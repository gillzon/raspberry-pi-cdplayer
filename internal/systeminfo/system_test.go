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
	if s.Uptime == nil || *s.Uptime != 12345.5 || s.Model != "Raspberry Pi 4 Model B" {
		t.Fatalf("system: %+v", s)
	}
	write("proc/stat", "cpu 1 0 0 1\n")
	m.sample()
	if m.Snapshot().CPUPercent != nil {
		t.Fatal("counter reset reported usage")
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
