package codelima

import "context"

// Group entry points for tests that exercise one command group directly.
//
// These are test scaffolding, not production dispatch: nothing in the CLI
// reaches a group except through Dispatch, which resolves the group from argv.
// They lived in cli.go, where a reader had to check every caller to discover
// that the only ones are _test.go files; keeping them here makes that obvious
// and keeps them out of the shipped binary.

func dispatchDaemon(ctx context.Context, service *Service, args []string) (any, error) {
	return dispatchGroup(ctx, service, findCLIGroup("daemon"), args)
}

func dispatchConfiguration(ctx context.Context, service *Service, args []string) (any, error) {
	return dispatchGroup(ctx, service, findCLIGroup("configuration"), args)
}

func dispatchNode(ctx context.Context, service *Service, args []string) (any, error) {
	return dispatchGroup(ctx, service, findCLIGroup("node"), args)
}
