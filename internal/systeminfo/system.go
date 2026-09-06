// Package systeminfo reads Linux counters without launching external commands.
package systeminfo

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Info struct {
	Hostname     string    `json:"hostname"`
	Model        string    `json:"model"`
	Architecture string    `json:"architecture"`
	CPUs         int       `json:"cpus"`
	CPUPercent   *float64  `json:"cpu_percent"`
	Temperature  *float64  `json:"temperature_c"`
	MemoryTotal  *uint64   `json:"memory_total"`
	MemoryUsed   *uint64   `json:"memory_used"`
	Uptime       *float64  `json:"uptime_seconds"`
	Updated      time.Time `json:"updated"`
}

type Monitor struct {
	mu                          sync.RWMutex
	info                        Info
	root                        string
	previousTotal, previousIdle uint64
	hasPrevious                 bool
}

func New() *Monitor { return &Monitor{root: "/"} }

func (m *Monitor) Snapshot() Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.info
}

func (m *Monitor) Run(ctx context.Context) {
	m.sample()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sample()
		}
	}
}

func (m *Monitor) read(path string) string {
	b, _ := os.ReadFile(filepath.Join(m.root, path))
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00"))
}

func (m *Monitor) sample() {
	hostname, _ := os.Hostname()
	info := Info{Hostname: hostname, Model: m.read("proc/device-tree/model"), Architecture: runtime.GOARCH, CPUs: runtime.NumCPU(), Updated: time.Now()}
	if total, idle, ok := cpuCounters(m.read("proc/stat")); ok {
		if m.hasPrevious && total > m.previousTotal && idle >= m.previousIdle && idle-m.previousIdle <= total-m.previousTotal {
			usage := 100 * float64((total-m.previousTotal)-(idle-m.previousIdle)) / float64(total-m.previousTotal)
			info.CPUPercent = &usage
		}
		m.previousTotal, m.previousIdle, m.hasPrevious = total, idle, true
	} else {
		m.hasPrevious = false
	}
	mem := make(map[string]uint64)
	for _, line := range strings.Split(m.read("proc/meminfo"), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			if value, err := strconv.ParseUint(f[1], 10, 64); err == nil {
				mem[strings.TrimSuffix(f[0], ":")] = value * 1024
			}
		}
	}
	if total, ok := mem["MemTotal"]; ok {
		info.MemoryTotal = &total
		if available, ok := mem["MemAvailable"]; ok && available <= total {
			used := total - available
			info.MemoryUsed = &used
		}
	}
	if f := strings.Fields(m.read("proc/uptime")); len(f) > 0 {
		if uptime, err := strconv.ParseFloat(f[0], 64); err == nil {
			info.Uptime = &uptime
		}
	}
	zones, _ := filepath.Glob(filepath.Join(m.root, "sys/class/thermal/thermal_zone*"))
	for _, zone := range zones {
		kind, _ := os.ReadFile(filepath.Join(zone, "type"))
		typeName := strings.ToLower(strings.TrimSpace(string(kind)))
		if !strings.Contains(typeName, "cpu") && !strings.Contains(typeName, "soc") && typeName != "bcm2835_thermal" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(zone, "temp"))
		if err != nil {
			continue
		}
		if value, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil {
			value /= 1000
			info.Temperature = &value
			break
		}
	}
	m.mu.Lock()
	m.info = info
	m.mu.Unlock()
}

func cpuCounters(input string) (total, idle uint64, ok bool) {
	line, _, _ := strings.Cut(input, "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	// guest and guest_nice are already included in user and nice.
	for i := 1; i < len(f) && i <= 8; i++ {
		n, err := strconv.ParseUint(f[i], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
		if i == 4 || i == 5 {
			idle += n
		}
	}
	return total, idle, true
}
