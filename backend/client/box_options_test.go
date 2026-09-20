package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/cachefile"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
)

func testPeers() (*config.Peer, *config.Peer) {
	game := &config.Peer{Name: "game", Protocol: "hysteria2", Addr: "203.0.113.9", Port: 5123, UUID: "secret"}
	httpPeer := &config.Peer{Name: "http", Protocol: "vless", Addr: "hk.example.com", Port: 443, UUID: "uuid"}
	return game, httpPeer
}

func buildTestOptions(t *testing.T, httpPeer *config.Peer) option.Options {
	t.Helper()
	game, defaultHTTP := testPeers()
	if httpPeer == nil {
		httpPeer = defaultHTTP
	}
	opts, err := BuildOptions(game, httpPeer, "https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query", nil)
	if err != nil {
		t.Fatal(err)
	}
	return opts
}

// TestRouteDefaultOutboundIsExplicit 守住一个隐蔽但关键的约定：
// sing-box 在 route.final 为空时会把"第一个出站"当成默认出站（adapter/outbound/manager.go:303），
// 未命中任何规则的流量（典型就是"纯 IP + UDP 的境外游戏服务器"）走的就是它。
// 所以 route.final 必须显式写死，且与第一个出站的 tag 一致。
func TestRouteDefaultOutboundIsExplicit(t *testing.T) {
	opts := buildTestOptions(t, nil)
	if opts.Route == nil {
		t.Fatal("缺少 route 配置")
	}
	if opts.Route.Final != proxyTag {
		t.Fatalf("route.final 必须显式等于 %q，实际 %q", proxyTag, opts.Route.Final)
	}
	if len(opts.Outbounds) == 0 {
		t.Fatal("没有出站")
	}
	if first := opts.Outbounds[0].Tag; first != opts.Route.Final {
		t.Fatalf("第一个出站 %q 与 route.final %q 不一致：一旦顺序被调整，默认走向会静默改变", first, opts.Route.Final)
	}
}

// TestRuleSetDownloadUsesGameProxy 规则更新必须固定走 Game 代理。
// Http 可以被用户明确设为直连；若规则更新跟着 Http 走，GitHub 在受限网络上会超时。
func TestRuleSetDownloadUsesGameProxy(t *testing.T) {
	opts := buildTestOptions(t, nil)
	tags := make(map[string]bool, len(opts.Outbounds))
	for _, outbound := range opts.Outbounds {
		tags[outbound.Tag] = true
	}
	for _, want := range []string{proxyTag, httpTag, directTag} {
		if !tags[want] {
			t.Fatalf("缺少出站 %q，实际出站: %v", want, tags)
		}
	}
	for _, ruleSet := range opts.Route.RuleSet {
		client := ruleSet.RemoteOptions.HTTPClient
		if client == nil || client.DialerOptions.Detour != proxyTag {
			t.Fatalf("规则集下载必须走 %q，实际 HTTPClient=%+v", proxyTag, client)
		}
		if ruleSet.RemoteOptions.DownloadDetour != "" {
			t.Fatalf("不应继续使用已弃用的 download_detour，实际 %q", ruleSet.RemoteOptions.DownloadDetour)
		}
	}
}

// TestMarkRuleSetsForRefresh 保留规则内容，只把更新时间标旧：
// sing-box 随后能立即用缓存启动，并在隧道启动后由 updater 后台拉取新版。
func TestMarkRuleSetsForRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	store := cachefile.New(context.Background(), log.NewNOPFactory().Logger(), option.CacheFileOptions{Path: path})
	if err := store.Start(adapter.StartStateInitialize); err != nil {
		t.Fatal(err)
	}
	wantContent := []byte("cached-rules")
	for _, tag := range managedRuleSetTags {
		if err := store.SaveRuleSet(tag, &adapter.SavedBinary{
			Content:     wantContent,
			LastUpdated: time.Now(),
			LastEtag:    "etag",
			URLHash:     []byte("hash"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := markRuleSetsForRefresh(path); err != nil {
		t.Fatal(err)
	}

	store = cachefile.New(context.Background(), log.NewNOPFactory().Logger(), option.CacheFileOptions{Path: path})
	if err := store.Start(adapter.StartStateInitialize); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tag := range managedRuleSetTags {
		got := store.LoadRuleSet(tag)
		if got == nil {
			t.Fatalf("规则缓存 %s 丢失", tag)
		}
		if string(got.Content) != string(wantContent) || got.LastEtag != "etag" {
			t.Fatalf("标记刷新不应破坏缓存内容: %+v", got)
		}
		if !got.LastUpdated.Before(time.Now().Add(-24*time.Hour)) || got.LastUpdated.IsZero() {
			t.Fatalf("规则 %s 应被标成非零的过期时间，实际 %s", tag, got.LastUpdated)
		}
	}
}

// TestClientSchedulesRuleRefresh 守住真实接线：Client 每次构造新隧道时都必须把缓存标旧，
// 不能只留下一个从未被调用的辅助函数。
func TestClientSchedulesRuleRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".gpp", "cache.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	store := cachefile.New(context.Background(), log.NewNOPFactory().Logger(), option.CacheFileOptions{Path: path})
	if err := store.Start(adapter.StartStateInitialize); err != nil {
		t.Fatal(err)
	}
	for _, tag := range managedRuleSetTags {
		if err := store.SaveRuleSet(tag, &adapter.SavedBinary{Content: []byte("cached"), LastUpdated: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	game, httpPeer := testPeers()
	instance, err := Client(game, httpPeer, "https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = instance.Close()

	store = cachefile.New(context.Background(), log.NewNOPFactory().Logger(), option.CacheFileOptions{Path: path})
	if err := store.Start(adapter.StartStateInitialize); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tag := range managedRuleSetTags {
		got := store.LoadRuleSet(tag)
		if got == nil || !got.LastUpdated.Before(time.Now().Add(-24*time.Hour)) {
			t.Fatalf("Client 未安排规则 %s 在启动后刷新: %+v", tag, got)
		}
	}
}

// TestPeerOwnAddressGoesDirect 节点自身地址必须直连：
// 否则"加速器去连节点"的连接会被自己的 TUN 再抓一次（环路/白耗带宽）。
func TestPeerOwnAddressGoesDirect(t *testing.T) {
	opts := buildTestOptions(t, nil)
	var foundCIDR, foundDomain bool
	for _, rule := range opts.Route.Rules {
		raw := rule.DefaultOptions.RawDefaultRule
		outbound := rule.DefaultOptions.RuleAction.RouteOptions.Outbound
		if outbound != directTag {
			continue
		}
		for _, cidr := range raw.IPCIDR {
			if cidr == "203.0.113.9/32" {
				foundCIDR = true
			}
		}
		for _, domain := range raw.Domain {
			if domain == "hk.example.com" {
				foundDomain = true
			}
		}
	}
	if !foundCIDR {
		t.Error("缺少「游戏节点 IP 直连」规则")
	}
	if !foundDomain {
		t.Error("缺少「网页节点域名直连」规则")
	}
}

// TestRouteRuleOrder 角色分工必须清晰：截断 QUIC 与大陆直连在用户规则之前，
// 网页/下载走 http 出站放在最后（优先级最低）。
func TestRouteRuleOrder(t *testing.T) {
	opts := buildTestOptions(t, nil)
	var rejectIndex, cnIndex, httpIndex = -1, -1, -1
	for i, rule := range opts.Route.Rules {
		raw := rule.DefaultOptions.RawDefaultRule
		action := rule.DefaultOptions.RuleAction
		switch {
		case action.Action == C.RuleActionTypeReject && rejectIndex == -1:
			rejectIndex = i
		case len(raw.RuleSet) > 0 && raw.RuleSet[0] == "geosite-cn" && cnIndex == -1:
			cnIndex = i
		case action.RouteOptions.Outbound == httpTag && httpIndex == -1:
			httpIndex = i
		}
	}
	if rejectIndex == -1 || cnIndex == -1 || httpIndex == -1 {
		t.Fatalf("规则缺失: reject=%d cn=%d http=%d", rejectIndex, cnIndex, httpIndex)
	}
	if !(rejectIndex < cnIndex && cnIndex < httpIndex) {
		t.Fatalf("规则顺序不符合预期: reject=%d cn=%d http=%d", rejectIndex, cnIndex, httpIndex)
	}
}

// TestBuildOptionsRejectsNilGamePeer 未选节点必须报错而不是 panic。
func TestBuildOptionsRejectsNilGamePeer(t *testing.T) {
	if _, err := BuildOptions(nil, nil, "https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query", nil); err == nil {
		t.Fatal("未选择节点时应当返回错误")
	}
}

// TestBuiltOptionsAcceptedBySingBox 生成的配置必须能被 sing-box 接受（只构造不启动）。
func TestBuiltOptionsAcceptedBySingBox(t *testing.T) {
	game, httpPeer := testPeers()
	instance, err := Client(game, httpPeer, "https://1.1.1.1/dns-query", "https://223.5.5.5/dns-query", nil)
	if err != nil {
		t.Fatalf("生成的配置无法通过 sing-box 校验: %v", err)
	}
	_ = instance.Close()
}
