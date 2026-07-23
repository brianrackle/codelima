//go:build !darwin

package codelima

func platformNestedVirtualizationSupported() bool {
	return false
}
