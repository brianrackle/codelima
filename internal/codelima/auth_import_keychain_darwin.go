//go:build darwin

package codelima

import (
	"context"
	"fmt"
	"os/exec"
)

// platformClaudeKeychainCredentials reads Claude Code's credential blob out of
// the macOS login Keychain, which is where the CLI keeps it on darwin instead
// of in a credentials file.
//
// `security` may raise an authorization prompt. The caller bounds ctx
// (hostAuthKeychainTimeout) precisely so an unanswered prompt cannot hang node
// creation; CommandContext kills the process when that deadline passes, and the
// caller treats the resulting error as an ordinary skip. Only stdout is read —
// stderr is deliberately left out of the error text so nothing the tool prints
// about the item can end up in a log or an event.
func platformClaudeKeychainCredentials(ctx context.Context) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", hostAuthKeychainService, "-w").Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("read the %s keychain item: %w", hostAuthKeychainService, ctxErr)
		}
		return nil, fmt.Errorf("read the %s keychain item: %w", hostAuthKeychainService, err)
	}
	// Returned verbatim: the collector owns normalization, so every seam
	// answers with exactly what the platform produced.
	return output, nil
}
