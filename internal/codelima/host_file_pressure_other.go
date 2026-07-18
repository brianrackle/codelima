//go:build !darwin

package codelima

func platformHostFilePressureSampler() (hostFilePressureSampler, bool) {
	return nil, false
}
