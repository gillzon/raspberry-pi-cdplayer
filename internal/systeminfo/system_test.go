package systeminfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSampling(t *testing.T) {
	m := &Monitor{root: t.TempDir()}
	write := func(path, contents string) {
		t.Helper()
		path = filepath.Join(m.root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/stat", "cpu 100 0 100 800 0 0 0 0 20 10\ncpu0 0 0 0 0\n")
	write("proc/meminfo", "MemTotal: 2048 kB\nMemAvailable: 512 kB\n")
	write("proc/uptime", "12345.5 9999\n")
	write("proc/net/route", "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\neth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\nwlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\n")
	write("proc/device-tree/model", "Raspberry Pi 4 Model B\x00")
	write("sys/class/thermal/thermal_zone0/type", "cpu-thermal\n")
	write("sys/class/thermal/thermal_zone0/temp", "42500\n")
	m.sample()
	if m.Snapshot().CPUPercent != nil {
		t.Fatal("CPU usage needs two samples")
	}
	write("proc/stat", "cpu 150 0 100 850 0 0 0 0 40 20\n")
	m.sample()
	s := m.Snapshot()
	if s.CPUPercent == nil || *s.CPUPercent != 50 {
		t.Fatalf("CPU: %+v", s)
	}
	if s.Temperature == nil || *s.Temperature != 42.5 {
		t.Fatalf("temperature: %+v", s)
	}
	if s.MemoryUsed == nil || *s.MemoryUsed != 1536*1024 || *s.MemoryTotal != 2048*1024 {
		t.Fatalf("memory: %+v", s)
	}
	if s.Uptime == nil || *s.Uptime != 12345.5 || s.Model != "Raspberry Pi 4 Model B" || s.Network != "Ethernet" {
		t.Fatalf("system: %+v", s)
	}
	write("proc/stat", "cpu 1 0 0 1\n")
	m.sample()
	if m.Snapshot().CPUPercent != nil {
		t.Fatal("counter reset reported usage")
	}
}

func TestActiveNetwork(t *testing.T) {
	for _, test := range []struct {
		name, routes, want string
	}{
		{"wifi", "Iface Destination Gateway Flags RefCnt Use Metric Mask\nwlan0 00000000 0101A8C0 0003 0 0 600 00000000\n", "Wi-Fi"},
		{"lowest metric", "Iface Destination Gateway Flags RefCnt Use Metric Mask\nwlan0 00000000 0101A8C0 0003 0 0 600 00000000\neth0 00000000 0101A8C0 0003 0 0 100 00000000\n", "Ethernet"},
		{"no default route", "Iface Destination Gateway Flags RefCnt Use Metric Mask\n", "Offline"},
		{"down route", "Iface Destination Gateway Flags RefCnt Use Metric Mask\neth0 00000000 0101A8C0 0000 0 0 100 00000000\n", "Offline"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := activeNetwork(test.routes); got != test.want {
				t.Fatalf("activeNetwork() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUnavailableMetrics(t *testing.T) {
	m := &Monitor{root: t.TempDir()}
	m.sample()
	s := m.Snapshot()
	if s.CPUPercent != nil || s.Temperature != nil || s.MemoryUsed != nil || s.Uptime != nil {
		t.Fatalf("missing values reported as measurements: %+v", s)
	}
}
