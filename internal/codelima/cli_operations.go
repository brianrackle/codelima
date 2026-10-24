package codelima

import (
	"context"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

// Configure lazy admission only on CLI lifecycle paths. Help and argument
// validation remain local, and services constructed by SDK callers stay inline.
func configureCLIOperations(service *Service, args []string) {
	if len(args) < 2 || args[0] != "node" {
		return
	}
	switch args[1] {
	case "create", "start", "retry", "stop", "clone", "delete":
		service.operationConnect = service.connectOperationDaemon
	}
}

func (s *Service) connectOperationDaemon(ctx context.Context) (*daemonclient.Client, error) {
	options := daemonclient.Options{Home: s.cfg.MetadataRoot, Version: Version}
	client, err := daemonclient.Dial(ctx, options)
	if err == nil {
		return client, nil
	}
	if !s.cfg.Daemon.Autostart {
		return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
	}
	_, startErr := startDaemon(ctx, s)
	// Another frontend may have won startup. The successful exact-version
	// handshake is authoritative; never fall back to inline lifecycle work.
	client, err = daemonclient.Dial(ctx, options)
	if err == nil {
		return client, nil
	}
	if startErr != nil {
		return nil, startErr
	}
	return nil, dependencyUnavailable("daemon did not accept the operation connection", err, nil)
}

func (s *Service) callOperationCLI(ctx context.Context, method string, params, result any) error {
	connect := s.operationConnect
	if connect == nil {
		connect = s.connectOperationDaemon
	}
	client, err := connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return fromDaemonError(client.Call(ctx, method, params, result))
}
