package client

import (
	"errors"
	"strings"
	"testing"

	"github.com/danbai225/gpp/backend/config"
)

// TestClientNilHTTPPeer 回归测试：未单独指定网页节点时（等价于 httpPeer 为 nil），
// 旧版本会在 httpPeer.Domain() 上空指针 panic（删除节点/订阅更新后最容易触发）。
func TestClientNilHTTPPeer(t *testing.T) {
	game := &config.Peer{
		Name:     "nil-http",
		Protocol: "vless",
		Addr:     "1.2.3.4",
		Port:     34555,
		UUID:     "123b22ef-1234-1234-1234-efeb224e03e7",
	}
	b, err := Client(game, nil, "https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query", nil)
	if err != nil {
		t.Fatalf("httpPeer 为 nil 时不应报错: %v", err)
	}
	_ = b.Close()
}

// TestClientNilGamePeer 未选节点时必须返回错误，而不是崩溃。
func TestClientNilGamePeer(t *testing.T) {
	if _, err := Client(nil, nil, "https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query", nil); err == nil {
		t.Fatal("未选择节点时应当返回错误")
	}
}

// TestClientNilGamePeerWithBoth 两个节点都为 nil 时也要走错误分支而不是 panic。
func TestClientPeerDirectRules(t *testing.T) {
	game := &config.Peer{Name: "ip节点", Protocol: "vless", Addr: "1.2.3.4", Port: 443}
	httpPeer := &config.Peer{Name: "域名节点", Protocol: "vless", Addr: "hk.example.com", Port: 443}

	rules := peerDirectRules(game, httpPeer)
	if len(rules) != 2 {
		t.Fatalf("应当生成 IP 与域名两条直连规则，实际 %d 条", len(rules))
	}

	// 直连节点不应被写入排除规则
	if got := peerDirectRules(&config.Peer{Name: "直连", Protocol: "direct", Addr: "127.0.0.1"}); len(got) != 0 {
		t.Fatalf("直连节点不应产生规则，实际 %d 条", len(got))
	}
}

func TestPeerDomainsFiltersIP(t *testing.T) {
	peers := []*config.Peer{
		{Name: "a", Addr: "1.2.3.4"},
		{Name: "b", Addr: "hk.example.com"},
		{Name: "c", Addr: "hk.example.com"},
		nil,
	}
	got := peerDomains(peers...)
	if len(got) != 1 || got[0] != "hk.example.com" {
		t.Fatalf("域名去重结果不对: %v", got)
	}
}

func TestExplainError(t *testing.T) {
	if ExplainError(nil) != nil {
		t.Fatal("nil 错误应当返回 nil")
	}
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("open tun: Access is denied."), "管理员"},
		{errors.New("listen tcp 127.0.0.1:5123: bind: Only one usage of each socket address is normally permitted."), "端口被占用"},
		{errors.New("download rule-set geosite-cn: dial tcp: i/o timeout"), "规则集"},
		{errors.New("hysteria2: authentication failed"), "握手/认证"},
	}
	for _, c := range cases {
		got := ExplainError(c.err).Error()
		if !strings.Contains(got, c.want) {
			t.Errorf("错误翻译结果缺少 %q：%s", c.want, got)
		}
		if !strings.Contains(got, c.err.Error()) {
			t.Errorf("错误翻译应当保留原始错误：%s", got)
		}
	}
	// 未知错误原样返回，避免误导
	raw := errors.New("some unknown failure")
	if ExplainError(raw).Error() != raw.Error() {
		t.Fatal("未知错误应当原样返回")
	}
}
