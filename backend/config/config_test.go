package config

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useTempConfig 把配置/订阅缓存重定向到临时目录，避免测试污染真实用户配置。
func useTempConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	SetPath(path)
	t.Cleanup(func() { SetPath("") })
	return path
}

func TestParsePeer(t *testing.T) {
	peer, err := ParsePeer("Z3BwOi8vdmxlc3NAMS4yLjMuNDozNDU1NS8xMjNiMjJlZi0xMjM0LTEyMzQtMTIzNC1lZmViMjI0ZTAzZTc=")
	if err != nil {
		t.Fatal(err)
	}
	if peer == nil {
		t.Fatal("peer is nil")
	}
	if peer.Protocol != "vless" || peer.Addr != "1.2.3.4" || peer.Port != 34555 {
		t.Fatalf("解析结果不对: %+v", peer)
	}
	if peer.Name != "1.2.3.4:34555" {
		t.Fatalf("默认名称不对: %s", peer.Name)
	}
}

func TestParsePeerEdgeCases(t *testing.T) {
	token := func(plain string) string { return base64.StdEncoding.EncodeToString([]byte(plain)) }

	t.Run("带名称片段", func(t *testing.T) {
		peer, err := ParsePeer(token("gpp://hysteria2@1.2.3.4:443/secret") + "#香港-01")
		if err != nil {
			t.Fatal(err)
		}
		if peer.Name != "香港-01" {
			t.Fatalf("名称不对: %s", peer.Name)
		}
	})

	t.Run("URL 安全且无 padding 的 base64", func(t *testing.T) {
		raw := base64.RawURLEncoding.EncodeToString([]byte("gpp://vless@1.2.3.4:443/uuid"))
		if _, err := ParsePeer(raw); err != nil {
			t.Fatalf("应该接受 URL 安全编码: %v", err)
		}
	})

	t.Run("IPv6 字面量", func(t *testing.T) {
		peer, err := ParsePeer(token("gpp://vless@[::1]:443/uuid"))
		if err != nil {
			t.Fatal(err)
		}
		if peer.Addr != "::1" || peer.Port != 443 {
			t.Fatalf("IPv6 解析不对: %+v", peer)
		}
	})

	for name, input := range map[string]string{
		"空内容":      "",
		"非 base64": "###not-base64###",
		"缺少 @":     token("gpp://vless1.2.3.4:443/uuid"),
		"缺少 uuid":  token("gpp://vless@1.2.3.4:443"),
		"端口非法":     token("gpp://vless@1.2.3.4:abc/uuid"),
		"协议不支持":    token("gpp://vmess@1.2.3.4:443/uuid"),
	} {
		if _, err := ParsePeer(input); err == nil {
			t.Errorf("%s：应当返回错误", name)
		}
	}
}

func TestPeerDomain(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"1.2.3.4", ""},
		{"1.2.3.4:443", ""},
		{"[::1]", ""},
		{"hk.example.com", "hk.example.com"},
		{"hk.example.com:443", "hk.example.com"},
		{"", ""},
	}
	for _, c := range cases {
		peer := &Peer{Addr: c.addr}
		if got := peer.Domain(); got != c.want {
			t.Errorf("Domain(%q) = %q, 期望 %q", c.addr, got, c.want)
		}
	}
	var nilPeer *Peer
	if got := nilPeer.Domain(); got != "" {
		t.Errorf("nil 节点应返回空串，实际 %q", got)
	}
	if got := nilPeer.Address(); got != "-" {
		t.Errorf("nil 节点 Address 应为 -，实际 %q", got)
	}
}

// TestLoadConfigInvalidFile 回归测试：配置损坏时必须返回可用配置而不是 nil，
// 旧版本会返回 nil 并让调用方在 conf.PeerList 上空指针崩溃。
func TestLoadConfigInvalidFile(t *testing.T) {
	path := useTempConfig(t)
	if err := os.WriteFile(path, []byte("{这不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	conf, err := LoadConfig()
	if conf == nil {
		t.Fatal("LoadConfig 不允许返回 nil 配置")
	}
	if err == nil {
		t.Fatal("配置损坏时应当返回提示性错误")
	}
	if !strings.Contains(err.Error(), "损坏") {
		t.Fatalf("错误信息应当说明配置损坏: %v", err)
	}
	if conf.FindPeer("直连") == nil {
		t.Fatal("重置后的默认配置应当包含直连节点")
	}
	backups, _ := filepath.Glob(path + ".bad-*")
	if len(backups) == 0 {
		t.Fatal("损坏的配置应当被备份")
	}
}

func TestLoadConfigFirstRun(t *testing.T) {
	path := useTempConfig(t)
	conf, err := LoadConfig()
	if conf == nil || err != nil {
		t.Fatalf("首次运行不应报错: conf=%v err=%v", conf, err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("首次运行应当落盘默认配置: %v", statErr)
	}
	if conf.GamePeer == "" || conf.HTTPPeer == "" {
		t.Fatalf("选中节点应当被自动补齐: %+v", conf)
	}
}

// TestLoadConfigSubscriptionFallback 订阅不可达时必须回退到本地缓存，且绝不返回 nil。
func TestLoadConfigSubscriptionFallback(t *testing.T) {
	path := useTempConfig(t)
	// 先写入订阅缓存（模拟上次成功拉取的结果）
	cached := []*Peer{{Name: "缓存节点", Protocol: "vless", Addr: "10.0.0.1", Port: 443, UUID: "x"}}
	data, _ := json.Marshal(subCache{UpdatedAt: time.Now().Add(-time.Hour), Peers: cached})
	if err := os.WriteFile(SubCachePath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.SubAddr = "http://127.0.0.1:9/sub"
	cfg.GamePeer = "缓存节点"
	cfg.HTTPPeer = "缓存节点"
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	conf, err := LoadConfig()
	if conf == nil {
		t.Fatal("LoadConfig 不允许返回 nil 配置")
	}
	if err == nil {
		t.Fatal("订阅不可达时应当返回提示性错误")
	}
	if !strings.Contains(err.Error(), "回退") {
		t.Fatalf("错误信息应当说明已回退到缓存: %v", err)
	}
	if conf.FindPeer("缓存节点") == nil {
		t.Fatal("应当使用缓存里的节点")
	}
	if conf.GamePeer != "缓存节点" {
		t.Fatalf("选中节点不应被改坏: %s", conf.GamePeer)
	}
}

func TestSaveConfigAtomicAndRoundTrip(t *testing.T) {
	path := useTempConfig(t)
	conf := Default()
	conf.PeerList = append(conf.PeerList, &Peer{Name: "hk", Protocol: "vless", Addr: "1.2.3.4", Port: 443, UUID: "u"})
	conf.GamePeer = "hk"
	conf.HTTPPeer = "hk"
	if err := SaveConfig(conf); err != nil {
		t.Fatal(err)
	}
	// 原子写不应留下临时文件
	if leftovers, _ := filepath.Glob(path + ".tmp-*"); len(leftovers) != 0 {
		t.Fatalf("原子写留下了临时文件: %v", leftovers)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.FindPeer("hk") == nil {
		t.Fatal("重新读取后节点丢失")
	}
	if loaded.GamePeer != "hk" {
		t.Fatalf("game_peer 未持久化: %s", loaded.GamePeer)
	}
}

// TestLoadConfigWithBOM Windows 记事本保存的 UTF-8 BOM 配置也必须能读。
func TestLoadConfigWithBOM(t *testing.T) {
	path := useTempConfig(t)
	body := `{"peer_list":[{"name":"hk","protocol":"vless","addr":"1.2.3.4","port":443}],"game_peer":"hk"}`
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, []byte(body)...), 0o644); err != nil {
		t.Fatal(err)
	}
	conf, err := LoadConfig()
	if err != nil {
		t.Fatalf("带 BOM 的配置不应被当成损坏: %v", err)
	}
	if conf.FindPeer("hk") == nil || conf.GamePeer != "hk" {
		t.Fatalf("带 BOM 的配置未正确加载: %+v", conf)
	}
}

// TestNormalizeFixesDanglingSelection 节点被删除/订阅更新后，选中节点必须自动校正。
func TestNormalizeFixesDanglingSelection(t *testing.T) {
	conf := &Config{
		PeerList: []*Peer{{Name: "hk", Protocol: "vless", Addr: "1.2.3.4", Port: 443}},
		GamePeer: "已经没了的节点",
		HTTPPeer: "另一个没了的节点",
	}
	warns := conf.Normalize()
	if conf.GamePeer != "hk" {
		t.Fatalf("GamePeer 应当回退到可用节点，实际 %s", conf.GamePeer)
	}
	if conf.HTTPPeer != "hk" {
		t.Fatalf("HTTPPeer 应当回退，实际 %s", conf.HTTPPeer)
	}
	if len(warns) == 0 {
		t.Fatal("自动切换应当给出提示")
	}
	if conf.FindPeer("直连") == nil {
		t.Fatal("应当补上直连节点")
	}
}

func TestNormalizeIsStable(t *testing.T) {
	conf := &Config{PeerList: []*Peer{
		{Name: "b", Protocol: "vless", Addr: "1.1.1.1", Port: 1},
		{Name: "b", Protocol: "vless", Addr: "1.1.1.2", Port: 2},
		{Name: "a", Protocol: "vless", Addr: "1.1.1.3", Port: 3},
	}}
	conf.Normalize()
	got := make([]string, 0, len(conf.PeerList))
	for _, p := range conf.PeerList {
		got = append(got, p.Name)
	}
	want := []string{"b", "a", "直连"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("节点顺序/去重结果不对: %v，期望 %v", got, want)
	}
}

func TestParseSubscription(t *testing.T) {
	peers := []*Peer{
		{Name: "hk", Protocol: "vless", Addr: "1.2.3.4", Port: 443},
		{Name: "jp", Protocol: "hysteria2", Addr: "5.6.7.8", Port: 8443},
		{Name: "坏节点", Protocol: "vless", Addr: "", Port: 443},
		{Name: "直连", Protocol: "direct", Addr: "127.0.0.1", Port: 0},
	}
	raw, _ := json.Marshal(peers)

	t.Run("JSON 数组", func(t *testing.T) {
		got, err := ParseSubscription(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("应当过滤掉无效节点与直连，实际 %d 个: %+v", len(got), got)
		}
	})

	t.Run("base64 数组", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString(raw)
		got, err := ParseSubscription([]byte(encoded))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("base64 订阅解析结果不对: %+v", got)
		}
	})

	t.Run("HTML 页面", func(t *testing.T) {
		if _, err := ParseSubscription([]byte("<!DOCTYPE html><html></html>")); err == nil {
			t.Fatal("非节点列表内容应当报错")
		}
	})

	t.Run("空内容", func(t *testing.T) {
		if _, err := ParseSubscription(nil); err == nil {
			t.Fatal("空内容应当报错")
		}
	})
}

func TestAddPeerAndDelPeer(t *testing.T) {
	useTempConfig(t)
	conf := Default()
	token := base64.StdEncoding.EncodeToString([]byte("gpp://vless@1.2.3.4:443/uuid"))

	if err := AddPeer(conf, token); err != nil {
		t.Fatal(err)
	}
	if len(conf.PeerList) != 2 { // 直连 + 新节点
		t.Fatalf("导入后节点数不对: %d", len(conf.PeerList))
	}
	if conf.GamePeer != "1.2.3.4:443" {
		t.Fatalf("首次导入应当自动选为新节点: %s", conf.GamePeer)
	}
	if err := AddPeer(conf, token); err == nil {
		t.Fatal("重复导入应当报错")
	}
	if err := AddPeer(conf, "https://127.0.0.1:9/sub"); err == nil {
		t.Fatal("订阅不可达时应当报错")
	}

	if err := DelPeer(conf, "1.2.3.4:443"); err != nil {
		t.Fatal(err)
	}
	if conf.FindPeer("1.2.3.4:443") != nil {
		t.Fatal("节点应当被删除")
	}
	if conf.GamePeer == "" || conf.FindPeer(conf.GamePeer) == nil {
		t.Fatalf("删除后选中节点必须指向存在的节点: %s", conf.GamePeer)
	}
	if err := DelPeer(conf, "不存在"); err == nil {
		t.Fatal("删除不存在的节点应当报错")
	}
	if err := DelPeer(conf, "直连"); err == nil {
		t.Fatal("内置直连节点不允许删除")
	}
}

func TestSaveConfigNil(t *testing.T) {
	useTempConfig(t)
	if err := SaveConfig(nil); err == nil {
		t.Fatal("SaveConfig(nil) 应当报错而不是 panic")
	}
}

func TestSubCacheRoundTrip(t *testing.T) {
	useTempConfig(t)
	peers := []*Peer{{Name: "x", Protocol: "vless", Addr: "1.1.1.1", Port: 443}}
	if err := writeSubCache(peers); err != nil {
		t.Fatal(err)
	}
	cache, err := readSubCache()
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Peers) != 1 || cache.Peers[0].Name != "x" {
		t.Fatalf("缓存内容不对: %+v", cache)
	}
	if cache.UpdatedAt.IsZero() {
		t.Fatal("缓存时间未写入")
	}
}
