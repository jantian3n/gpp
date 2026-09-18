//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const enableVirtualTerminalProcessing = 0x0004

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")

	shell32           = syscall.NewLazyDLL("shell32.dll")
	procIsUserAnAdmin = shell32.NewProc("IsUserAnAdmin")
)

// enableANSI 尝试为当前控制台打开 ANSI（VT）转义支持。
// 失败时返回 false，界面自动退化为"只追加不刷屏"的样式，而不是打印一堆乱码。
func enableANSI() bool {
	handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || handle == 0 {
		return false
	}
	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	if r, _, _ := procSetConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing)); r == 0 {
		return false
	}
	return true
}

// isAdmin 判断当前进程是否具备管理员权限（TUN 需要）。
func isAdmin() bool {
	r, _, _ := procIsUserAnAdmin.Call()
	return r != 0
}
