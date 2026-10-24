package codelima

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func metricsSSHPeer(t *testing.T, accept func(ssh.NewChannel, <-chan struct{})) *sshForwardingPeer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		server, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer func() { _ = server.Close() }()
		go ssh.DiscardRequests(requests)
		for channel := range channels {
			accept(channel, stop)
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "guest", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		close(stop)
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(stop)
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("SSH fixture failed to stop")
		}
	})
	return &sshForwardingPeer{client: client}
}

func TestNodeMetricsProcessSSHBoundsChannelOpening(t *testing.T) {
	peer := metricsSSHPeer(t, func(channel ssh.NewChannel, stop <-chan struct{}) {
		<-stop
		_ = channel.Reject(ssh.ConnectionFailed, "test ended")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := peer.runBounded(ctx, "ignored", 1024)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("channel open exceeded deadline: %v", err)
	}
}

func TestNodeMetricsProcessSSHBoundsOutput(t *testing.T) {
	peer := metricsSSHPeer(t, func(channel ssh.NewChannel, _ <-chan struct{}) {
		stream, requests, err := channel.Accept()
		if err != nil {
			return
		}
		defer func() { _ = stream.Close() }()
		for request := range requests {
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			_ = request.Reply(true, nil)
			_, _ = stream.Write([]byte(strings.Repeat("x", 2048)))
			_, _ = stream.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			return
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := peer.runBounded(ctx, "ignored", 1024)
	if err == nil || !strings.Contains(err.Error(), "exceeds 1024 bytes") || data != nil {
		t.Fatalf("oversized output accepted: %d bytes, %v", len(data), err)
	}
}
