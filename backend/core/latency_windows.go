//go:build windows

package core

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

var (
	iphlpapi            = syscall.NewLazyDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
)

// icmpLatency 用系统自带的 IcmpSendEcho 量一次 ICMP 往返（毫秒）。
//
// 这里刻意不用原始套接字（golang.org/x/net/icmp）：在 Windows 上开原始 ICMP 套接字
// 需要管理员权限，而 IcmpSendEcho 是系统提供的普通 API（ping.exe 用的就是它），
// 普通权限也能用。这样即使以后 GUI 以非提权方式作为前端运行，延迟一样测得出。
func icmpLatency(host string) uint {
	ip := resolveIPv4(host)
	if ip == nil {
		return 0
	}

	handle, _, _ := procIcmpCreateFile.Call()
	// IcmpCreateFile 失败时返回 INVALID_HANDLE_VALUE(-1)
	if handle == 0 || handle == ^uintptr(0) {
		return 0
	}
	defer func() { _, _, _ = procIcmpCloseHandle.Call(handle) }()

	request := []byte("gpp-latency")
	// 回包缓冲区用来放 ICMP_ECHO_REPLY + 请求数据；多留空间比算精确布局更安全
	reply := make([]byte, 128+len(request))

	// IPAddr 是网络字节序的 IPv4 地址
	dest := binary.BigEndian.Uint32(ip)
	timeout := uint32(icmpProbeTimeout.Milliseconds())

	count, _, _ := procIcmpSendEcho.Call(
		handle,
		uintptr(dest),
		uintptr(unsafe.Pointer(&request[0])),
		uintptr(len(request)),
		0, // 请求选项用默认值
		uintptr(unsafe.Pointer(&reply[0])),
		uintptr(len(reply)),
		uintptr(timeout),
	)
	if count == 0 {
		// 超时或不可达：IcmpSendEcho 会返回 0 并设置 GetLastError
		return 0
	}

	// ICMP_ECHO_REPLY: Address(4) Status(4) RoundTripTime(4) ... 均为 ULONG（本机字节序）
	if len(reply) < 12 {
		return 0
	}
	if status := binary.LittleEndian.Uint32(reply[4:8]); status != 0 {
		return 0 // 非 IP_SUCCESS：目的不可达、TTL 超时等
	}
	return uint(binary.LittleEndian.Uint32(reply[8:12]))
}
