//go:build windows

package platform

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

type comGUID struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}
type comObject struct{ vtable *[32]uintptr }

var clsidFileOpenDialog = comGUID{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
var iidFileOpenDialog = comGUID{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}

func PickFolder() (string, bool, error) {
	type result struct {
		path     string
		canceled bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		path, canceled, err := pickFolderNative()
		done <- result{path, canceled, err}
	}()
	r := <-done
	return r.path, r.canceled, r.err
}

func comCall(object *comObject, index int, args ...uintptr) uintptr {
	method := object.vtable[index]
	call := append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)
	result, _, _ := syscall.SyscallN(method, call...)
	return result
}

func dialogStatus(status uintptr) (bool, error) {
	code := uint32(status)
	if code == 0x800704C7 {
		return true, nil
	}
	if int32(code) < 0 {
		return false, fmt.Errorf("диалог выбора папки: HRESULT 0x%08X", code)
	}
	return false, nil
}

func pickFolderNative() (string, bool, error) {
	ole := syscall.NewLazyDLL("ole32.dll")
	status, _, _ := ole.NewProc("CoInitializeEx").Call(0, 2)
	if _, err := dialogStatus(status); err != nil {
		return "", false, err
	}
	defer ole.NewProc("CoUninitialize").Call()
	var dialog *comObject
	status, _, _ = ole.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, 1, uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(&dialog)))
	if _, err := dialogStatus(status); err != nil {
		return "", false, err
	}
	if dialog == nil {
		return "", false, fmt.Errorf("пустой интерфейс диалога")
	}
	defer comCall(dialog, 2)
	var options uint32
	if _, err := dialogStatus(comCall(dialog, 10, uintptr(unsafe.Pointer(&options)))); err != nil {
		return "", false, err
	}
	// FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM | FOS_PATHMUSTEXIST | FOS_NOCHANGEDIR.
	options |= 0x20 | 0x40 | 0x800 | 0x8
	if _, err := dialogStatus(comCall(dialog, 9, uintptr(options))); err != nil {
		return "", false, err
	}
	title, _ := syscall.UTF16PtrFromString("Выберите папку с шаблонами запросов")
	if _, err := dialogStatus(comCall(dialog, 17, uintptr(unsafe.Pointer(title)))); err != nil {
		return "", false, err
	}
	canceled, err := dialogStatus(comCall(dialog, 3, 0))
	if canceled || err != nil {
		return "", canceled, err
	}
	var item *comObject
	if _, err = dialogStatus(comCall(dialog, 20, uintptr(unsafe.Pointer(&item)))); err != nil {
		return "", false, err
	}
	if item == nil {
		return "", false, fmt.Errorf("диалог не вернул папку")
	}
	defer comCall(item, 2)
	var path *uint16
	if _, err = dialogStatus(comCall(item, 5, 0x80058000, uintptr(unsafe.Pointer(&path)))); err != nil {
		return "", false, err
	}
	if path == nil {
		return "", false, fmt.Errorf("папка не имеет пути файловой системы")
	}
	defer ole.NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(path)))
	units := make([]uint16, 0, 260)
	for i := uintptr(0); i < 32768; i++ {
		unit := *(*uint16)(unsafe.Add(unsafe.Pointer(path), 2*i))
		if unit == 0 {
			return syscall.UTF16ToString(units), false, nil
		}
		units = append(units, unit)
	}
	return "", false, fmt.Errorf("путь папки превышает ограничение Windows")
}
