//go:build darwin

package telemetry

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func (p *Sampler) hardware(ctx context.Context, h *Hardware) {
	for key, target := range map[string]**string{"hw.model": &h.Model, "machdep.cpu.brand_string": &h.Chip} {
		if out, err := p.run(ctx, "/usr/sbin/sysctl", "-n", key); err == nil {
			value := strings.TrimSpace(string(out))
			if value != "" && len(value) <= 256 {
				*target = &value
			}
		}
	}
	if out, err := p.run(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize"); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil && n > 0 {
			h.MemoryTotalBytes = &n
		}
	}
}

func (p *Sampler) host(ctx context.Context, s *Sample) {
	// iostat's first row is a lifetime average; only use the second interval.
	if out, err := p.run(ctx, "/usr/sbin/iostat", "-c", "2", "-w", "1"); err != nil {
		s.Quality["cpu_percent"] = quality(err)
	} else if v, err := parseCPU(string(out)); err != nil {
		s.Quality["cpu_percent"] = Invalid
	} else {
		s.CPUPercent = &v
		s.Quality["cpu_percent"] = OK
	}

	if out, err := p.run(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize"); err != nil {
		s.Quality["memory_total_bytes"] = quality(err)
	} else if v, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err != nil || v <= 0 {
		s.Quality["memory_total_bytes"] = Invalid
	} else {
		s.MemoryTotalBytes = &v
		s.Quality["memory_total_bytes"] = OK
	}
	if out, err := p.run(ctx, "/usr/bin/vm_stat"); err != nil {
		s.Quality["memory_available_bytes"] = quality(err)
	} else if v, err := parseAvailable(string(out)); err != nil {
		s.Quality["memory_available_bytes"] = Invalid
	} else {
		s.MemoryAvailableBytes = &v
		s.Quality["memory_available_bytes"] = OK
	}
	if out, err := p.run(ctx, "/usr/sbin/sysctl", "-n", "kern.memorystatus_vm_pressure_level"); err != nil {
		s.Quality["memory_pressure"] = quality(err)
	} else {
		value := map[string]string{"1": "normal", "2": "warning", "4": "critical"}[strings.TrimSpace(string(out))]
		if value == "" {
			s.Quality["memory_pressure"] = Invalid
		} else {
			s.MemoryPressure = &value
			s.Quality["memory_pressure"] = OK
		}
	}
	if out, err := p.run(ctx, "/usr/sbin/sysctl", "-n", "vm.swapusage"); err != nil {
		s.Quality["swap_used_bytes"] = quality(err)
	} else if v, err := parseSwap(string(out)); err != nil {
		s.Quality["swap_used_bytes"] = Invalid
	} else {
		s.SwapUsedBytes = &v
		s.Quality["swap_used_bytes"] = OK
	}
	if out, err := p.run(ctx, "/usr/sbin/ioreg", "-r", "-d", "1", "-c", "IOAccelerator"); err != nil {
		s.Quality["gpu_percent"] = quality(err)
	} else if v, ok := parseGPU(string(out)); ok {
		s.GPUPercent = &v
		s.Quality["gpu_percent"] = OK
	}
	// Foundation exposes thermal pressure without root or an installed helper.
	if out, err := p.run(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", "ObjC.import('Foundation'); Number($.NSProcessInfo.processInfo.thermalState)"); err != nil {
		s.Quality["thermal_state"] = quality(err)
	} else {
		value := map[string]string{"0": "nominal", "1": "fair", "2": "serious", "3": "critical"}[strings.TrimSpace(string(out))]
		if value == "" {
			s.Quality["thermal_state"] = Invalid
		} else {
			s.ThermalState = &value
			s.Quality["thermal_state"] = OK
		}
	}
	// No verified Celsius sensor source is implemented in this build.
	// Keep temperatures null/unsupported rather than inventing sensor units.
	var disk unix.Statfs_t
	if err := unix.Statfs(p.DiskPath, &disk); err != nil {
		s.Quality["disk_available_bytes"] = quality(err)
	} else if disk.Bsize <= 0 || disk.Bavail > math.MaxInt64/uint64(disk.Bsize) {
		s.Quality["disk_available_bytes"] = Invalid
	} else {
		s.DiskAvailableBytes = Ptr(int64(disk.Bavail) * int64(disk.Bsize))
		s.Quality["disk_available_bytes"] = OK
	}
}

func processAlive(pid int, start *int64) (bool, error) {
	if err := unix.Kill(pid, 0); err != nil {
		if errors.Is(err, unix.ESRCH) {
			return false, nil
		}
		return false, err
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return false, err
	}
	if start != nil && k.Proc.P_starttime.Sec*1_000_000+int64(k.Proc.P_starttime.Usec) != *start {
		return false, nil
	}
	name := unix.ByteSliceToString(k.Proc.P_comm[:])
	if !strings.EqualFold(name, "darkbloom") {
		return false, nil
	}
	return k.Proc.P_stat != 5, nil // SZOMB
}

func parseCPU(out string) (float64, error) {
	var rows [][]string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if _, err := strconv.ParseFloat(fields[0], 64); err == nil {
			rows = append(rows, fields)
		}
	}
	if len(rows) < 2 {
		return 0, errors.New("missing interval CPU sample")
	}
	last := rows[len(rows)-1]
	idle, err := strconv.ParseFloat(last[len(last)-4], 64)
	if err != nil || math.IsNaN(idle) || idle < 0 || idle > 100 {
		return 0, errors.New("invalid CPU sample")
	}
	return 100 - idle, nil
}

var pageSizeRE = regexp.MustCompile(`page size of ([0-9]+) bytes`)

func parseAvailable(out string) (int64, error) {
	m := pageSizeRE.FindStringSubmatch(out)
	if len(m) != 2 {
		return 0, errors.New("missing page size")
	}
	page, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || page <= 0 {
		return 0, errors.New("invalid page size")
	}
	var pages int64
	found := 0
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || (key != "Pages free" && key != "Pages inactive" && key != "Pages speculative") {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
		if err != nil || n < 0 || n > math.MaxInt64-pages {
			return 0, errors.New("invalid page count")
		}
		pages += n
		found++
	}
	if found != 3 || pages > math.MaxInt64/page {
		return 0, errors.New("incomplete memory sample")
	}
	return pages * page, nil
}

var swapRE = regexp.MustCompile(`used\s*=\s*([0-9]+(?:\.[0-9]+)?)([KMGT])`)

func parseSwap(out string) (int64, error) {
	m := swapRE.FindStringSubmatch(out)
	if len(m) != 3 {
		return 0, errors.New("invalid swap sample")
	}
	v, err := strconv.ParseFloat(m[1], 64)
	v *= map[string]float64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[m[2]]
	if err != nil || math.IsInf(v, 0) || v >= math.MaxInt64 {
		return 0, errors.New("swap overflow")
	}
	return int64(math.Round(v)), nil
}

var gpuRE = regexp.MustCompile(`"(?:Device Utilization %|GPU Activity\(%\))"\s*=\s*([0-9]+(?:\.[0-9]+)?)`)

func parseGPU(out string) (float64, bool) {
	var maximum float64
	found := false
	for _, m := range gpuRE.FindAllStringSubmatch(out, -1) {
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil || v < 0 || v > 100 {
			continue
		}
		maximum = math.Max(maximum, v)
		found = true
	}
	return maximum, found
}

func (p *Sampler) processCPU(ctx context.Context, pid int) (*float64, string) {
	out, err := p.run(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "pcpu=")
	if err != nil {
		return nil, quality(err)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return nil, Invalid
	}
	return &v, OK
}
