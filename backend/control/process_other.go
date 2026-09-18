//go:build !windows

package control

import (
	"errors"
	"syscall"
)

// processAlive 判断 PID 对应的进程是否仍然存在（signal 0 只做存在性检查）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}
