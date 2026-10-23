//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var ErrAlreadyRunning = errors.New("Highloader уже запущен")

type Owner struct {
	Dir      string
	handle   syscall.Handle
	once     sync.Once
	closeErr error
}

func AcquireOwner(dir string) (*Owner, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// EvalSymlinks enumerates restricted ancestors on Windows.
	// CreateFile resolves aliases itself; the exclusive handle is the identity.
	canonical := filepath.Clean(absolute)
	path, err := syscall.UTF16PtrFromString(filepath.Join(canonical, "owner.lock"))
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, syscall.Errno(32)) { // ERROR_SHARING_VIOLATION is not exported by syscall.
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("блокировка каталога: %w", err)
	}
	return &Owner{Dir: canonical, handle: handle}, nil
}

func (o *Owner) Close() error {
	o.once.Do(func() { o.closeErr = syscall.CloseHandle(o.handle) })
	return o.closeErr
}
