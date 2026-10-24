package hostinfo

import (
	"golang.org/x/sys/unix"
	"testing"
)

func TestLinuxVolumeUsesFragmentSize(t *testing.T) {
	// VirtioFS can advertise 1 MiB optimal I/O transfers while its block
	// counts use 4 KiB fragments. Mixing them overstates free space 256x.
	stat := unix.Statfs_t{Bsize: 1 << 20, Frsize: 4096, Blocks: 100, Bavail: 25}
	total, available := volumeBytes(stat)
	if total == nil || available == nil || *total != 409600 || *available != 102400 {
		t.Fatalf("volume units incorrect: total=%v available=%v", total, available)
	}
}

func TestLinuxMemoryUsesTotalAndRejectsInvalidUnitsAndOverflow(t *testing.T) {
	for _, test := range []struct {
		input string
		want  uint64
	}{
		{"MemAvailable: 77 kB\nMemTotal: 1024 kB\n", 1048576},
		{"MemTotal: 0 kB\n", 0},
		{"MemTotal: -1 kB\n", 0},
		{"MemTotal: 123 MB\n", 0},
		{"MemTotal: 18446744073709551615 kB\n", 0},
		{"MemAvailable: 123 kB\n", 0},
	} {
		got := linuxMemoryTotal([]byte(test.input))
		if test.want == 0 {
			if got != nil {
				t.Errorf("%q: invalid memory became %d", test.input, *got)
			}
		} else if got == nil || *got != test.want {
			t.Errorf("%q: want %d, got %v", test.input, test.want, got)
		}
	}
}

func TestLinuxCPUModelPrefersModelNameThenHardware(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"processor: 0\nmodel name : AMD EPYC\nmodel name : AMD EPYC\n", "AMD EPYC"},
		{"processor: 0\nCPU implementer: 0x61\nHardware: Virtual Machine\n", "Virtual Machine"},
		{"Hardware: Board\nmodel name: CPU\n", "CPU"},
		{"processor: 0\nCPU architecture: 8\n", ""},
	} {
		if got := linuxCPUModel([]byte(test.input)); got != test.want {
			t.Errorf("model=%q, want %q", got, test.want)
		}
	}
}
