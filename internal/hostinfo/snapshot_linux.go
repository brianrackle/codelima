package hostinfo

import (
	"math"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func volumeBlockSize(stat unix.Statfs_t) uint64 {
	if stat.Frsize > 0 {
		return uint64(stat.Frsize)
	}
	if stat.Bsize > 0 {
		return uint64(stat.Bsize)
	}
	return 0
}

func platformCapacity() (string, *uint64) {
	cpu, _ := os.ReadFile("/proc/cpuinfo")
	model := linuxCPUModel(cpu)
	if model == "" {
		// ARM systems often expose no model name in cpuinfo.
		for _, path := range []string{"/proc/device-tree/model", "/sys/devices/virtual/dmi/id/product_name"} {
			data, err := os.ReadFile(path)
			if err == nil {
				model = strings.Trim(strings.TrimSpace(string(data)), "\x00")
				if model != "" {
					break
				}
			}
		}
	}
	memory, _ := os.ReadFile("/proc/meminfo")
	return model, linuxMemoryTotal(memory)
}

func linuxCPUModel(data []byte) string {
	hardware := ""
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name":
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		case "Hardware":
			hardware = strings.TrimSpace(value)
		}
	}
	return hardware
}

func linuxMemoryTotal(data []byte) *uint64 {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "MemTotal:" || fields[2] != "kB" {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || kb == 0 || kb > math.MaxUint64/1024 {
			return nil
		}
		bytes := kb * 1024
		return &bytes
	}
	return nil
}
