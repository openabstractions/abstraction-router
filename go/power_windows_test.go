package router

import (
	"fmt"
	"syscall"
	"unsafe"
)

type powerStatus struct {
	ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag byte
	BatteryLifeTime, BatteryFullLifeTime                            uint32
}

// powerState is the state line every measurement on this machine carries: two
// figures quoted all week were taken from a laptop at 7% with battery saver on
// and recorded neither.
func powerState() string {
	var s powerStatus
	r, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemPowerStatus").
		Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return "power state unreadable: " + err.Error()
	}
	source := map[byte]string{0: "on battery", 1: "on AC", 255: "unknown"}[s.ACLineStatus]
	saver := ""
	if s.SystemStatusFlag == 1 {
		saver = ", battery saver ON"
	}
	return fmt.Sprintf("%s, battery %d%%%s", source, s.BatteryLifePercent, saver)
}
