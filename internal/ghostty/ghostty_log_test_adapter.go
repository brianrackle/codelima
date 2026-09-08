//go:build cgo && (darwin || linux)

package ghostty

// #include "ghostty_bridge_compat.h"
import "C"

import "unsafe"

// cgo cannot be imported from a _test.go file. This deliberately small adapter
// exercises the real native callback and bounded sink used by production.
func emitGhosttyTestLog(message []byte) {
	if len(message) == 0 {
		return
	}
	C.ghostty_bridge_test_log((*C.uint8_t)(unsafe.Pointer(&message[0])), C.size_t(len(message)))
}

func readGhosttyTestLog(capacity int) []byte {
	buffer := make([]byte, capacity)
	if len(buffer) == 0 {
		return buffer
	}
	n := int(C.ghostty_bridge_read_log((*C.uint8_t)(unsafe.Pointer(&buffer[0])), C.size_t(len(buffer))))
	return buffer[:n]
}
