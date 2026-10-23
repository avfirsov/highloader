//go:build windows

package app

import (
	"context"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/platform"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Sampler struct {
	latest atomic.Pointer[engine.CPUValue]
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	err    error
}

func NewSampler(sample func() platform.CPUValue, closeCounter func() error) *Sampler {
	s := &Sampler{stop: make(chan struct{}), done: make(chan struct{})}
	s.latest.Store(&engine.CPUValue{Reason: "Ожидание первого измерения CPU"})
	go func() {
		defer close(s.done)
		defer func() {
			if closeCounter != nil {
				s.err = closeCounter()
			}
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var sequence uint64
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				v := sample()
				sequence++
				if v.At.IsZero() {
					v.At = time.Now().UTC()
				}
				if v.Percent != nil {
					x := *v.Percent
					v.Percent = &x
				}
				s.latest.Store(&engine.CPUValue{ID: strconv.FormatUint(sequence, 10), At: v.At, Percent: v.Percent, Reason: v.MissingReason})
			}
		}
	}()
	return s
}
func (s *Sampler) Latest() engine.CPUValue {
	v := *s.latest.Load()
	if v.Percent != nil {
		x := *v.Percent
		v.Percent = &x
	}
	return v
}
func (s *Sampler) Close() error { s.once.Do(func() { close(s.stop) }); <-s.done; return s.err }

func OpenNative(ctx context.Context, dir string, o Options) (*Application, error) {
	historyDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		return nil, err
	}
	store, err := history.Open(filepath.Join(historyDir, "highloader.sqlite"))
	if err != nil {
		return nil, err
	}
	if _, err = store.Recover(ctx, time.Now().UTC()); err != nil {
		store.Close()
		return nil, err
	}
	cpu, err := platform.OpenCPU()
	var sampler *Sampler
	if err != nil {
		sampler = NewSampler(func() platform.CPUValue {
			return platform.CPUValue{MissingReason: "Счётчик CPU недоступен"}
		}, nil)
	} else {
		sampler = NewSampler(cpu.Sample, cpu.Close)
	}
	o.Store = store
	o.PickFolder = platform.PickFolder
	o.CPU = sampler
	o.CloseCPU = sampler.Close
	a, err := New(o)
	if err != nil {
		sampler.Close()
		store.Close()
		return nil, err
	}
	return a, nil
}
