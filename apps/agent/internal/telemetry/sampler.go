package telemetry

import (
	"context"
	"io"
	"os"
	"runtime"
	"syscall"
	"time"
)

type Sampler struct {
	StatePath string
	DiskPath  string
	run       commandFunc
	read      func(string) ([]byte, error)
	process   func(int, *int64) (bool, error)
}

func NewSampler(statePath, diskPath string) *Sampler {
	return &Sampler{StatePath: statePath, DiskPath: diskPath, run: command, read: readState, process: processAlive}
}

func readState(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, os.ErrInvalid
	}
	return io.ReadAll(io.LimitReader(f, (1<<20)+1))
}

func (p *Sampler) Hardware(ctx context.Context) Hardware {
	h := Hardware{OS: runtime.GOOS, Architecture: runtime.GOARCH, LogicalCPUs: runtime.NumCPU()}
	p.hardware(ctx, &h)
	return h
}

func (p *Sampler) Collect(ctx context.Context, now time.Time) Sample {
	started := time.Now()
	s := NewSample(now)
	p.host(ctx, &s)
	p.provider(ctx, &s)
	s.DurationMS = time.Since(started).Milliseconds()
	return s
}
