//go:build darwin

package daemon

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// RequireSameUserPeer rejects a Unix connection not owned by the daemon's
// effective user. It is authoritative when a shared filesystem ignores mode.
func RequireSameUserPeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("daemon connection is not a Unix socket")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var peerUID uint32
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			credentialErr = err
			return
		}
		peerUID = credentials.Uid
	}); err != nil {
		return err
	}
	if credentialErr != nil {
		return credentialErr
	}
	if peerUID != uint32(os.Geteuid()) {
		return fmt.Errorf("daemon peer uid %d does not match owner uid %d", peerUID, os.Geteuid())
	}
	return nil
}
