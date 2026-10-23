//go:build windows

package platform

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func DefaultDataDir() (string, error) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		return "", fmt.Errorf("переменная LOCALAPPDATA отсутствует")
	}
	return filepath.Join(root, "Highloader"), nil
}

func OpenBrowser(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		return fmt.Errorf("недопустимый локальный адрес")
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	target, err := syscall.UTF16PtrFromString(address)
	if err != nil {
		return err
	}
	result, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
	if result <= 32 {
		return fmt.Errorf("не удалось открыть браузер (код %d)", result)
	}
	return nil
}

func ShowMessage(title, message string) error {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return err
	}
	m, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	result, _, err := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
	if result == 0 {
		return err
	}
	return nil
}
