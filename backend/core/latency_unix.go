//go:build !windows

package core

import (
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// icmpLatency 用原始 ICMP 套接字量一次往返（毫秒）。
//
// Linux/macOS 上开原始 ICMP 套接字需要 root，而 gpp 因为要建 TUN 网卡本来就得以
// sudo 运行，所以这个前提是满足的。万一没有权限，ListenPacket 会返回错误，
// 这里返回 0，界面显示"未测速"，不会崩。
func icmpLatency(host string) uint {
	ip := resolveIPv4(host)
	if ip == nil {
		return 0
	}
	dst := &net.IPAddr{IP: ip}
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return 0
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(icmpProbeTimeout)); err != nil {
		return 0
	}

	// PingAll 会并发探测所有节点，而原始 ICMP 套接字能收到所有回包，
	// 所以每次用不同的 ID，并且只认 ID+Seq 都匹配的那一个。
	id := uint16(os.Getpid()) ^ uint16(time.Now().UnixNano())
	const seq = 1
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: int(id), Seq: seq, Data: []byte("gpp-latency")},
	}
	payload, err := msg.Marshal(nil)
	if err != nil {
		return 0
	}

	start := time.Now()
	if _, err := conn.WriteTo(payload, dst); err != nil {
		return 0
	}

	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return 0
		}
		reply, err := icmp.ParseMessage(ipv4.ICMPTypeEcho.Protocol(), buf[:n])
		if err != nil || reply.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := reply.Body.(*icmp.Echo)
		if !ok || echo.ID != int(id) || echo.Seq != seq {
			continue // 别的节点/别的进程的回包
		}
		return uint(time.Since(start).Milliseconds())
	}
}
