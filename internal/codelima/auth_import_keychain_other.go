//go:build !darwin

package codelima

import (
	"context"
	"fmt"
)

// platformClaudeKeychainCredentials has no non-darwin implementation: Claude
// Code writes ~/.claude/.credentials.json on these hosts, which the collector
// already prefers, so reaching here means there is nothing left to read. The
// error is reported as a skip, never as a failure.
func platformClaudeKeychainCredentials(context.Context) ([]byte, error) {
	return nil, fmt.Errorf("reading the %s keychain item is only supported on darwin hosts", hostAuthKeychainService)
}
