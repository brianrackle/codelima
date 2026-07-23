//go:build darwin && cgo

package codelima

/*
#cgo LDFLAGS: -framework Foundation -framework Virtualization
int codelima_nested_virtualization_supported(void);
*/
import "C"

func platformNestedVirtualizationSupported() bool {
	return C.codelima_nested_virtualization_supported() == 1
}
