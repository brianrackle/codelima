//go:build darwin && !cgo

package codelima

func platformNestedVirtualizationSupported() bool {
	return false
}
