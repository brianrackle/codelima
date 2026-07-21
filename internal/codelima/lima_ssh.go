package codelima

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// LimaSSHRuntime exposes only the runtime metadata needed by dynamic
// forwarding. The generated per-instance SSH config remains Lima-owned.
type LimaSSHRuntime interface {
	ForwardingSSHConfig(context.Context, string) (LimaSSHConfig, error)
}

func (c *LimaClient) ForwardingSSHConfig(ctx context.Context, sandboxName string) (LimaSSHConfig, error) {
	if err := validateSandboxName(sandboxName); err != nil {
		return LimaSSHConfig{}, err
	}
	observations, err := c.List(ctx)
	if err != nil {
		return LimaSSHConfig{}, err
	}
	observation, ok := findObservation(observations, sandboxName)
	if !ok {
		return LimaSSHConfig{}, notFound("Lima instance not found for dynamic forwarding", map[string]any{"sandbox_name": sandboxName})
	}
	if observation.Status != ObservationRunning {
		return LimaSSHConfig{}, preconditionFailed("Lima instance must be running for dynamic forwarding", map[string]any{"sandbox_name": sandboxName})
	}
	if observation.SSHConfigFile == "" || observation.LimaHome == "" {
		return LimaSSHConfig{}, metadataCorruption("limactl list omitted SSH connection metadata", nil, map[string]any{"sandbox_name": sandboxName})
	}
	return parseLimaSSHConfig(observation.SSHConfigFile, observation.LimaHome)
}

type limaSSHForwardingPeerFactory struct{ runtime LimaSSHRuntime }

func (f limaSSHForwardingPeerFactory) Prepare(context.Context) error { return nil }

func (f limaSSHForwardingPeerFactory) Connect(ctx context.Context, node Node) (forwardingPeer, error) {
	config, err := f.runtime.ForwardingSSHConfig(ctx, node.SandboxName)
	if err != nil {
		return nil, err
	}
	privateKey, err := os.ReadFile(config.IdentityFile)
	if err != nil {
		return nil, dependencyUnavailable("read Lima SSH identity", err, map[string]any{"path": config.IdentityFile})
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, metadataCorruption("parse Lima SSH identity", err, map[string]any{"path": config.IdentityFile})
	}
	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, externalCommandFailed("connect to Lima SSH endpoint", err, map[string]any{"sandbox_name": node.SandboxName, "address": address})
	}
	deadline := time.Now().Add(10 * time.Second)
	_ = connection.SetDeadline(deadline)
	clientConfig := &ssh.ClientConfig{
		User:            config.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Safe here: parseLimaSSHConfig requires Lima-owned config and an exact loopback endpoint.
		Timeout:         10 * time.Second,
	}
	sshConnection, channels, requests, err := ssh.NewClientConn(connection, address, clientConfig)
	if err != nil {
		_ = connection.Close()
		return nil, externalCommandFailed("handshake with Lima SSH endpoint", err, map[string]any{"sandbox_name": node.SandboxName})
	}
	_ = connection.SetDeadline(time.Time{})
	client := ssh.NewClient(sshConnection, channels, requests)
	if client == nil {
		_ = connection.Close()
		return nil, fmt.Errorf("create Lima SSH client")
	}
	return &sshForwardingPeer{client: client}, nil
}
