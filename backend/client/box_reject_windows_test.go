//go:build windows

package client

import (
	"net"
	"testing"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"golang.org/x/sys/windows"
)

// TestRejectUDP443 回归测试：向 TUN 注入 UDP/443 数据包（命中阻断 QUIC 的 reject 规则）。
// sing-box 1.14 中 reject 未指定 method 会在 sing-tun 的 goroutine 中 panic，
// 导致整个进程闪退且无任何提示；指定 method 后不得再出现。
func TestRejectUDP443(t *testing.T) {
	isAdmin := windows.NewLazySystemDLL("shell32.dll").NewProc("IsUserAnAdmin")
	r, _, _ := isAdmin.Call()
	if r == 0 {
		t.Skip("需要管理员权限创建 TUN 网卡")
	}
	game := &config.Peer{
		Name:     "reject-test",
		Protocol: "vless",
		Addr:     "1.2.3.4",
		Port:     34555,
		UUID:     "123b22ef-1234-1234-1234-efeb224e03e7",
	}
	b, err := Client(game, game, "https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	// 该数据包经系统路由进入 TUN，必命中 udp/443 reject 规则；
	// 若 method 缺失，sing-box 会在此处 panic 使测试进程崩溃。
	conn, err := net.DialTimeout("udp", "1.1.1.1:443", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("probe"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Read(make([]byte, 1))
	time.Sleep(time.Second) // 等待路由处理完成
}
