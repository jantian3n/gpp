//go:build !windows

package main

import "os"

// enableANSI：非 Windows 终端基本都支持 ANSI 转义。
func enableANSI() bool {
	return os.Getenv("TERM") != "" || os.Getenv("WT_SESSION") != ""
}

// isAdmin：Linux/macOS 上用 euid 判断（TUN 需要 root）。
func isAdmin() bool {
	return os.Geteuid() == 0
}
