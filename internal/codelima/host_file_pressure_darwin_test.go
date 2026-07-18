//go:build darwin

package codelima

import "testing"

func TestPlatformHostFilePressureSamplerReadsDarwinSysctls(t *testing.T) {
	t.Parallel()
	sample, supported := platformHostFilePressureSampler()
	if !supported {
		t.Fatal("Darwin host file pressure sampler is unsupported")
	}
	pressure, err := sample()
	if err != nil {
		t.Fatalf("sample() error = %v", err)
	}
	if pressure.used == 0 || pressure.limit == 0 || pressure.used > pressure.limit {
		t.Fatalf("invalid host file pressure sample: %+v", pressure)
	}
}
