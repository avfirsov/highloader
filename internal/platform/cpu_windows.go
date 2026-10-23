//go:build windows

package platform

import (
	"fmt"
	"math"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type CPUValue struct {
	Percent       *float64  `json:"percent"`
	At            time.Time `json:"at"`
	MissingReason string    `json:"missingReason,omitempty"`
}
type CPU struct {
	mu             sync.Mutex
	query, counter uintptr
	pdh            *syscall.LazyDLL
	closed         bool
}
type pdhValue struct {
	Status uint32
	_      uint32
	Double float64
}

func OpenCPU() (*CPU, error) {
	pdh := syscall.NewLazyDLL("pdh.dll")
	for _, name := range []string{"PdhOpenQueryW", "PdhAddEnglishCounterW", "PdhCollectQueryData", "PdhGetFormattedCounterValue", "PdhCloseQuery"} {
		if err := pdh.NewProc(name).Find(); err != nil {
			return nil, err
		}
	}
	c := &CPU{pdh: pdh}
	status, _, _ := pdh.NewProc("PdhOpenQueryW").Call(0, 0, uintptr(unsafe.Pointer(&c.query)))
	if status != 0 {
		return nil, fmt.Errorf("PDH: открытие запроса 0x%08X", uint32(status))
	}
	path, _ := syscall.UTF16PtrFromString(`\Processor Information(_Total)\% Processor Time`)
	status, _, _ = pdh.NewProc("PdhAddEnglishCounterW").Call(c.query, uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&c.counter)))
	if status != 0 {
		c.Close()
		return nil, fmt.Errorf("PDH: общий счётчик CPU недоступен 0x%08X", uint32(status))
	}
	status, _, _ = pdh.NewProc("PdhCollectQueryData").Call(c.query)
	if status != 0 {
		c.Close()
		return nil, fmt.Errorf("PDH: исходный отсчёт недоступен 0x%08X", uint32(status))
	}
	return c, nil
}

func cpuValue(at time.Time, status uintptr, counterStatus uint32, value float64) CPUValue {
	result := CPUValue{At: at}
	if status != 0 || counterStatus > 1 {
		result.MissingReason = fmt.Sprintf("CPU недоступен: PDH 0x%08X, статус 0x%08X", uint32(status), counterStatus)
		return result
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		result.MissingReason = "CPU недоступен: неверное значение счётчика"
		return result
	}
	result.Percent = &value
	return result
}

func (c *CPU) Sample() CPUValue {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	if c.closed {
		return CPUValue{At: now, MissingReason: "Счётчик CPU закрыт"}
	}
	status, _, _ := c.pdh.NewProc("PdhCollectQueryData").Call(c.query)
	if status != 0 {
		return cpuValue(now, status, 0, 0)
	}
	var value pdhValue
	status, _, _ = c.pdh.NewProc("PdhGetFormattedCounterValue").Call(c.counter, 0x200, 0, uintptr(unsafe.Pointer(&value)))
	return cpuValue(now, status, value.Status, value.Double)
}

func (c *CPU) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	status, _, _ := c.pdh.NewProc("PdhCloseQuery").Call(c.query)
	if status != 0 {
		return fmt.Errorf("PDH: закрытие запроса 0x%08X", uint32(status))
	}
	return nil
}
