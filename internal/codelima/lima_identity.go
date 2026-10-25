package codelima

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
)

// Lima 2.1 checks for its private key before taking its key-generation lock,
// without rechecking afterward. Two first starts can therefore both invoke
// ssh-keygen, with the loser refusing to overwrite the winner's key. Let Lima
// create its own identity, holding a separate cross-process gate only until
// the complete pair is observable. VM boot itself remains parallel.
func (c *LimaClient) withIdentityInitialization(ctx context.Context, run func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := c.resolvedLimaHome()
	if err != nil {
		return err
	}
	config := filepath.Join(home, "_config")
	if limaIdentityReady(config) {
		return run()
	}
	if err := os.MkdirAll(config, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(config, ".codelima-start.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := flockWithContext(ctx, file, nil, "Lima SSH identity initialization"); err != nil {
		_ = file.Close()
		return err
	}
	if limaIdentityReady(config) {
		_ = file.Close()
		return run()
	}
	done, released := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(released)
		defer func() { _ = file.Close() }()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if limaIdentityReady(config) {
					return
				}
			}
		}
	}()
	defer func() { close(done); <-released }()
	return run()
}

func limaIdentityReady(config string) bool {
	private, err := os.Stat(filepath.Join(config, "user"))
	if err != nil || !private.Mode().IsRegular() || private.Size() == 0 {
		return false
	}
	path := filepath.Join(config, "user.pub")
	public, err := os.Stat(path)
	if err != nil || !public.Mode().IsRegular() || public.Size() > 16<<10 {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, _, _, _, err = ssh.ParseAuthorizedKey(data)
	return err == nil
}
