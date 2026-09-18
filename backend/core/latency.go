package core

import (
	"fmt"
	"net"
	"time"
)

const (
	tcpProbeTimeout  = 3 * time.Second
	icmpProbeTimeout = 2 * time.Second
)

// udpOnlyProtocols 只监听 UDP 的协议。对它们做 TCP 握手一定失败，所以直接走 ICMP：
// 既测得出延迟，也省掉一次必然超时的等待。
//
// 这里多列了几个当前还没支持的协议名，只是为了以后加协议时不会再犯同一个错。
var udpOnlyProtocols = map[string]bool{
	"hysteria":  true,
	"hysteria2": true,
	"tuic":      true,
	"wireguard": true,
}

// measureLatency 量一次节点延迟（毫秒），测不出来返回 0。
//
// 为什么不能只用 TCP：hysteria2/tuic 这类协议只在 UDP 上监听，TCP 永远握手失败。
// 老实现对所有节点都做 TCP 探测，于是这类节点被一律显示成"未测速" —— 而它们恰恰是
// 加速器里最常见的线路。现在改成按协议选探测方式。
//
// 返回 0 的含义和以前一致：没测出来（节点离线、服务端不回 ICMP、没有解析出 IPv4 等），
// 界面显示"未测速"，不要当成 0ms。
func measureLatency(protocol, host string, port uint16) uint {
	if udpOnlyProtocols[protocol] {
		return icmpLatency(host)
	}
	if ms := tcpLatency(host, port); ms > 0 {
		return ms
	}
	// 服务端屏蔽了 TCP 探测（或协议名不认识）时，退回 ICMP 兜底
	return icmpLatency(host)
}

// tcpLatency 单次 TCP 握手耗时（毫秒）。握手耗时就是用户能感知到的连接延迟。
func tcpLatency(host string, port uint16) uint {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), tcpProbeTimeout)
	if err != nil {
		return 0
	}
	_ = conn.Close()
	return uint(time.Since(start).Milliseconds())
}

// resolveIPv4 把节点地址解析成 IPv4；解析不出来返回 nil。
func resolveIPv4(host string) net.IP {
	if ip := net.ParseIP(host); ip != nil {
		return ip.To4()
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		if v4 := addr.To4(); v4 != nil {
			return v4
		}
	}
	return nil
}
