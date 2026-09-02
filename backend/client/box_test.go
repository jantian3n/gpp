package client

import (
	"testing"

	"github.com/danbai225/gpp/backend/config"
)

// TestClientConfig 验证各协议下生成的 sing-box 配置能通过 box.New 校验
func TestClientConfig(t *testing.T) {
	protocols := []string{"vless", "shadowsocks", "socks", "hysteria2", "direct"}
	for _, protocol := range protocols {
		game := &config.Peer{
			Name:     "test-" + protocol,
			Protocol: protocol,
			Addr:     "1.2.3.4",
			Port:     34555,
			UUID:     "123b22ef-1234-1234-1234-efeb224e03e7",
		}
		httpPeer := &config.Peer{
			Name:     "test-http",
			Protocol: "vless",
			Addr:     "5.6.7.8",
			Port:     34556,
			UUID:     "123b22ef-1234-1234-1234-efeb224e03e7",
		}
		b, err := Client(game, httpPeer, "https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query", nil)
		if err != nil {
			t.Errorf("%s: %v", protocol, err)
			continue
		}
		_ = b.Close()
	}
}
