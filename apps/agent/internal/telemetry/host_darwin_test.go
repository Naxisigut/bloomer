//go:build darwin

package telemetry

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMacOSMetricParsingAndPartialFailure(t *testing.T) {
	iostat := "disk0 cpu load average\nKB/t tps MB/s us sy id 1m 5m 15m\n29.29 84 2.41 7 4 89 1.56 2.12 2.09\n6.02 129 0.75 12 3 85 1.56 2.12 2.09\n"
	if v, err := parseCPU(iostat); err != nil || v != 15 {
		t.Fatalf("CPU interval: %v %v", v, err)
	}
	for _, body := range []string{"", "1 2 3 4 5 6\n", "1 2 3 1 2 NaN 4 5 6\n1 2 3 1 2 101 4 5 6"} {
		if _, err := parseCPU(body); err == nil {
			t.Fatalf("accepted incomplete CPU: %s", body)
		}
	}
	vm := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 10.\nPages inactive: 20.\nPages speculative: 3.\nPages active: 999.\n"
	if v, err := parseAvailable(vm); err != nil || v != 33*16384 {
		t.Fatalf("memory: %v %v", v, err)
	}
	if _, err := parseAvailable("page size of 16384 bytes\nPages free: 10."); err == nil {
		t.Fatal("incomplete memory became zero")
	}
	if v, err := parseSwap("total = 2048.00M used = 412.50M free = 1M"); err != nil || v != 432537600 {
		t.Fatalf("swap: %v %v", v, err)
	}
	if v, ok := parseGPU(`"PerformanceStatistics"={"Device Utilization %"=0}`); !ok || v != 0 {
		t.Fatal("valid zero GPU missing")
	}
	if _, ok := parseGPU(`"Allocated PB Size"=98566144`); ok {
		t.Fatal("memory mistaken for GPU utilization")
	}
	p := NewSampler("missing", t.TempDir())
	p.run = func(_ context.Context, path string, _ ...string) ([]byte, error) {
		if path == "/usr/sbin/iostat" {
			return []byte(iostat), nil
		}
		return nil, os.ErrPermission
	}
	p.read = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	s := p.Collect(context.Background(), time.Now())
	if s.CPUPercent == nil || *s.CPUPercent != 15 || s.DiskAvailableBytes == nil {
		t.Fatal("one failure erased other metrics")
	}
	if s.MemoryTotalBytes != nil || s.Quality["memory_total_bytes"] != PermissionDenied || s.CPUTemperatureC != nil || s.Quality["cpu_temperature_c"] != Unsupported {
		t.Fatalf("missing became zero: %+v", s)
	}
}
