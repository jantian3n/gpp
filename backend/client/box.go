package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/google/uuid"
	box "github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// directOptions 构造直连出站选项。
// 指定通过本地 DNS 解析目标域名，使其不再是"空出站"：
// sing-box 1.14 会拒绝 DNS 等 detour 指向空直连出站（本地 DNS 的 detour 即为 direct）。
func directOptions() *option.DirectOutboundOptions {
	return &option.DirectOutboundOptions{
		DialerOptions: option.DialerOptions{
			AbstractDialerOptions: option.AbstractDialerOptions{
				DomainResolver: &option.DomainResolveOptions{Server: "localDns"},
			},
		},
	}
}

// LogOutput 非空时，sing-box 运行日志写入该文件（info 级别），用于排查问题；
// debug 配置开启时仍会覆盖为 trace 级别输出到 debug.log。
var LogOutput string

func multiplexOptions() *option.OutboundMultiplexOptions {
	return &option.OutboundMultiplexOptions{
		Enabled:        true,
		Protocol:       "h2mux",
		MaxConnections: 16,
		MinStreams:     32,
		Padding:        false,
	}
}

func getOUt(peer *config.Peer) option.Outbound {
	serverOptions := option.ServerOptions{
		Server:     peer.Addr,
		ServerPort: peer.Port,
	}
	var out option.Outbound
	switch peer.Protocol {
	case "shadowsocks":
		out = option.Outbound{
			Type: "shadowsocks",
			Options: &option.ShadowsocksOutboundOptions{
				ServerOptions: serverOptions,
				Method:        "aes-256-gcm",
				Password:      peer.UUID,
				UDPOverTCP: &option.UDPOverTCPOptions{
					Enabled: true,
					Version: 2,
				},
				Multiplex: multiplexOptions(),
			},
		}
	case "socks":
		out = option.Outbound{
			Type: "socks",
			Options: &option.SOCKSOutboundOptions{
				ServerOptions: serverOptions,
				Username:      "gpp",
				Password:      peer.UUID,
				UDPOverTCP: &option.UDPOverTCPOptions{
					Enabled: true,
					Version: 2,
				},
			},
		}
	case "hysteria2":
		out = option.Outbound{
			Type: "hysteria2",
			Options: &option.Hysteria2OutboundOptions{
				ServerOptions: serverOptions,
				Password:      peer.UUID,
				OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
					TLS: &option.OutboundTLSOptions{
						Enabled:    true,
						ServerName: "gpp",
						Insecure:   true,
						ALPN:       badoption.Listable[string]{"h3"},
					},
				},
				BrutalDebug: false,
			},
		}
	case "direct":
		out = option.Outbound{
			Type:    "direct",
			Options: directOptions(),
		}
	default:
		out = option.Outbound{
			Type: "vless",
			Options: &option.VLESSOutboundOptions{
				ServerOptions: serverOptions,
				UUID:          peer.UUID,
				Multiplex:     multiplexOptions(),
			},
		}
	}
	out.Tag = uuid.New().String()
	return out
}

// listenAddr 构造监听地址
func listenAddr(s string) *badoption.Addr {
	addr := badoption.Addr(netip.MustParseAddr(s))
	return &addr
}

// dnsServerPort 从 URL 取端口，缺省返回 defaultPort
func dnsServerPort(u *url.URL, defaultPort uint16) uint16 {
	if port := u.Port(); port != "" {
		var p uint16
		_, _ = fmt.Sscanf(port, "%d", &p)
		if p != 0 {
			return p
		}
	}
	return defaultPort
}

// buildDNSServer 将 URL 形式的 DNS 地址（如 https://1.1.1.1/dns-query）转换为新版 DNS 服务器配置
func buildDNSServer(tag, address, detour string) (option.DNSServerOptions, error) {
	dialer := option.DialerOptions{Detour: detour}
	u, err := url.Parse(address)
	if err != nil {
		return option.DNSServerOptions{}, err
	}
	switch u.Scheme {
	case "https":
		path := u.Path
		if path == "" {
			path = "/dns-query"
		}
		return option.DNSServerOptions{
			Type: C.DNSTypeHTTPS,
			Tag:  tag,
			Options: &option.RemoteHTTPSDNSServerOptions{
				RemoteTLSDNSServerOptions: option.RemoteTLSDNSServerOptions{
					RemoteDNSServerOptions: option.RemoteDNSServerOptions{
						RawLocalDNSServerOptions: option.RawLocalDNSServerOptions{DialerOptions: dialer},
						DNSServerAddressOptions:  option.DNSServerAddressOptions{Server: u.Hostname(), ServerPort: dnsServerPort(u, 443)},
					},
				},
				Path: path,
			},
		}, nil
	case "tls":
		return option.DNSServerOptions{
			Type: C.DNSTypeTLS,
			Tag:  tag,
			Options: &option.RemoteTLSDNSServerOptions{
				RemoteDNSServerOptions: option.RemoteDNSServerOptions{
					RawLocalDNSServerOptions: option.RawLocalDNSServerOptions{DialerOptions: dialer},
					DNSServerAddressOptions:  option.DNSServerAddressOptions{Server: u.Hostname(), ServerPort: dnsServerPort(u, 853)},
				},
			},
		}, nil
	case "udp", "":
		host := u.Hostname()
		if u.Scheme == "" {
			host = address
			if h, _, splitErr := net.SplitHostPort(address); splitErr == nil {
				host = h
			}
		}
		return option.DNSServerOptions{
			Type: C.DNSTypeUDP,
			Tag:  tag,
			Options: &option.RemoteDNSServerOptions{
				RawLocalDNSServerOptions: option.RawLocalDNSServerOptions{DialerOptions: dialer},
				DNSServerAddressOptions:  option.DNSServerAddressOptions{Server: host, ServerPort: dnsServerPort(u, 53)},
			},
		}, nil
	default:
		return option.DNSServerOptions{}, fmt.Errorf("unsupported dns server scheme: %s", u.Scheme)
	}
}

// dnsRouteRule 构造 DNS 路由规则：命中条件后使用 server 解析
func dnsRouteRule(rule option.RawDefaultDNSRule, server string) option.DNSRule {
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: rule,
			DNSRuleAction: option.DNSRuleAction{
				Action:       C.RuleActionTypeRoute,
				RouteOptions: option.DNSRouteActionOptions{Server: server},
			},
		},
	}
}

// routeRule 构造路由规则：命中条件后出站至 outbound
func routeRule(rule option.RawDefaultRule, outbound string) option.Rule {
	return option.Rule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultRule{
			RawDefaultRule: rule,
			RuleAction: option.RuleAction{
				Action:       C.RuleActionTypeRoute,
				RouteOptions: option.RouteActionOptions{Outbound: outbound},
			},
		},
	}
}

// remoteRuleSet 构造远程规则集引用
func remoteRuleSet(tag, url string) option.RuleSet {
	return option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Tag:    badoption.Listable[string]{tag},
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL:            url,
			DownloadDetour: "http",
		},
	}
}

// logOptions 构造日志配置：默认关闭；设置了 LogOutput 时输出到该文件。
func logOptions() *option.LogOptions {
	if LogOutput == "" {
		return &option.LogOptions{Disabled: true}
	}
	return &option.LogOptions{
		Disabled:     false,
		Level:        "info",
		Output:       LogOutput,
		Timestamp:    true,
		DisableColor: true,
	}
}

func Client(gamePeer, httpPeer *config.Peer, proxyDNS, localDNS string, rules []option.Rule) (*box.Box, error) {
	if gamePeer == nil {
		return nil, errors.New("未选择游戏节点")
	}
	// 未单独指定网页节点时复用游戏节点：这里必须显式兜底，
	// 否则后续 httpPeer.Domain() 会空指针 panic（旧版本在"节点被删除/订阅更新"后就会崩）。
	if httpPeer == nil {
		httpPeer = gamePeer
	}
	proxyOut := getOUt(gamePeer)
	// 必须是独立对象：若两个 tag 指向同一个出站，先设置的 tag 会被后一次赋值覆盖，
	// 导致 DownloadDetour 引用的 "http" 出站不存在。
	httpOut := getOUt(httpPeer)
	httpOut.Tag = "http"
	proxyOut.Tag = "proxy"

	// 规则集缓存：geosite/geoip 规则集下载一次后落盘，
	// 避免每次启动都依赖网络下载（弱网/断网时也能用缓存启动）。
	cachePath := filepath.Join(config.UserDir(), "cache.db")
	_ = os.MkdirAll(filepath.Dir(cachePath), 0o755)

	proxyDNSServer, err := buildDNSServer("proxyDns", proxyDNS, "proxy")
	if err != nil {
		return nil, err
	}
	localDNSServer, err := buildDNSServer("localDns", localDNS, "direct")
	if err != nil {
		return nil, err
	}

	options := box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Experimental: &option.ExperimentalOptions{
				CacheFile: &option.CacheFileOptions{
					Enabled: true,
					Path:    cachePath,
				},
			},
			Log: logOptions(),
			DNS: &option.DNSOptions{
				RawDNSOptions: option.RawDNSOptions{
					Servers: []option.DNSServerOptions{
						proxyDNSServer,
						localDNSServer,
					},
					Rules: dnsRules(gamePeer, httpPeer),
					Final: "proxyDns",
					DNSClientOptions: option.DNSClientOptions{
						Strategy:     option.DomainStrategy(C.DomainStrategyIPv4Only),
						DisableCache: true,
					},
				},
			},
			Inbounds: []option.Inbound{
				{
					Type: "tun",
					Tag:  "tun-in",
					Options: &option.TunInboundOptions{
						InterfaceName: "utun225",
						MTU:           9000,
						Address: badoption.Listable[netip.Prefix]{
							netip.MustParsePrefix("172.25.0.1/30"),
						},
						AutoRoute:   true,
						StrictRoute: true,
						UDPMapping:  option.UDPNATBehaviorEndpointIndependent,
						UDPTimeout:  option.UDPTimeoutCompat(time.Second * 300),
						Stack:       "system",
					},
				},
				{
					Type: "socks",
					Tag:  "socks-in",
					Options: &option.SocksInboundOptions{
						ListenOptions: option.ListenOptions{
							Listen:     listenAddr("127.0.0.1"),
							ListenPort: 5123,
						},
					},
				},
			},
			Route: &option.RouteOptions{
				AutoDetectInterface: true,
				RuleSet: []option.RuleSet{
					remoteRuleSet("geosite-cn", "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs"),
					remoteRuleSet("geosite-geolocation-!cn", "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-!cn.srs"),
					remoteRuleSet("geoip-cn", "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"),
				},
				Rules: []option.Rule{
					{
						Type: C.RuleTypeDefault,
						DefaultOptions: option.DefaultRule{
							RuleAction: option.RuleAction{
								Action: C.RuleActionTypeSniff,
							},
						},
					},
					{
						Type: C.RuleTypeDefault,
						DefaultOptions: option.DefaultRule{
							RawDefaultRule: option.RawDefaultRule{
								Protocol: badoption.Listable[string]{"dns"},
							},
							RuleAction: option.RuleAction{
								Action: C.RuleActionTypeHijackDNS,
							},
						},
					},
				},
			},
			Outbounds: []option.Outbound{
				proxyOut,
				httpOut,
				{
					Type:    "direct",
					Tag:     "direct",
					Options: directOptions(),
				},
			},
		},
	}

	// 节点自身地址必须在最前面直连，避免"加速器连节点的连接又被自己抓进隧道"
	options.Route.Rules = append(options.Route.Rules, peerDirectRules(gamePeer, httpPeer)...)
	options.Route.Rules = append(options.Route.Rules, []option.Rule{
		// 阻断 UDP 443，避免浏览器/应用走 QUIC 绕过代理
		{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					Network: badoption.Listable[string]{"udp"},
					Port:    badoption.Listable[uint16]{443},
				},
				RuleAction: option.RuleAction{
					Action: C.RuleActionTypeReject,
					// sing-box 1.14 中 reject 必须指定 method，否则处理 UDP 包时 panic
					RejectOptions: option.RejectActionOptions{
						Method: C.RuleActionRejectMethodDefault,
					},
				},
			},
		},
		routeRule(option.RawDefaultRule{
			RuleSet: badoption.Listable[string]{"geosite-cn"},
		}, "direct"),
		routeRule(option.RawDefaultRule{
			RuleSet: badoption.Listable[string]{"geoip-cn"},
		}, "direct"),
		routeRule(option.RawDefaultRule{
			IPIsPrivate: true,
		}, "direct"),
		routeRule(option.RawDefaultRule{
			IPCIDR: badoption.Listable[string]{
				"85.236.96.0/21",
				"188.42.95.0/24",
				"188.42.147.0/24"},
		}, "direct"),
		routeRule(option.RawDefaultRule{
			DomainSuffix: badoption.Listable[string]{
				"vivox.com",
				"cm.steampowered.com",
				"steamchina.com",
				"steamcontent.com",
				"steamserver.net",
				"steamusercontent.com",
				"csgo.wmsj.cn",
				"dl.steam.clngaa.com",
				"dl.steam.ksyna.com",
				"dota2.wmsj.cn",
				"st.dl.bscstorage.net",
				"st.dl.eccdnx.com",
				"st.dl.pinyuncloud.com",
				"steampipe.steamcontent.tnkjmec.com",
				"steampowered.com.8686c.com",
				"steamstatic.com.8686c.com",
				"wmsjsteam.com",
				"xz.pphimalayanrt.com"},
		}, "direct"),
	}...)
	options.Route.Rules = append(options.Route.Rules, rules...)
	// http
	if httpPeer != nil && httpPeer.Name != gamePeer.Name {
		options.Route.Rules = append(options.Route.Rules, routeRule(option.RawDefaultRule{Protocol: badoption.Listable[string]{"http"}}, httpOut.Tag))
		options.Route.Rules = append(options.Route.Rules, routeRule(option.RawDefaultRule{Network: badoption.Listable[string]{"tcp"}, Port: badoption.Listable[uint16]{80, 443, 8080, 8443}}, httpOut.Tag))
	}
	if config.Debug.Load() {
		options.Log = &option.LogOptions{
			Disabled:     false,
			Level:        "trace",
			Output:       filepath.Join(config.UserDir(), "debug.log"),
			Timestamp:    true,
			DisableColor: true,
		}
		content, err := options.Options.MarshalJSONContext(options.Context)
		if err == nil {
			var buf bytes.Buffer
			if json.Indent(&buf, content, "", " ") == nil {
				_ = os.WriteFile(filepath.Join(config.UserDir(), "sing.json"), buf.Bytes(), 0o644)
			}
		}
	}
	var instance *box.Box
	instance, err = box.New(options)
	if err != nil {
		return nil, err
	}
	return instance, nil
}

// dnsRules 构造 DNS 路由规则：选中节点自己的域名用本地 DNS 解析（否则会自己解析自己），
// CN 域名走本地 DNS，境外域名走代理 DNS。
func dnsRules(gamePeer, httpPeer *config.Peer) []option.DNSRule {
	rules := make([]option.DNSRule, 0, 3)
	if domains := peerDomains(gamePeer, httpPeer); len(domains) > 0 {
		rules = append(rules, dnsRouteRule(option.RawDefaultDNSRule{
			Domain: badoption.Listable[string](domains),
		}, "localDns"))
	}
	return append(rules,
		dnsRouteRule(option.RawDefaultDNSRule{
			RuleSet: badoption.Listable[string]{"geosite-cn"},
		}, "localDns"),
		dnsRouteRule(option.RawDefaultDNSRule{
			RuleSet: badoption.Listable[string]{"geosite-geolocation-!cn"},
		}, "proxyDns"),
	)
}

// peerDomains 收集节点里真正的域名；IP 节点会被 Domain() 过滤为空串，
// 避免往 DNS 规则里塞"placeholder.com"这类假域名。
func peerDomains(peers ...*config.Peer) []string {
	seen := make(map[string]bool, len(peers))
	domains := make([]string, 0, len(peers))
	for _, p := range peers {
		domain := p.Domain()
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		domains = append(domains, domain)
	}
	return domains
}

// peerDirectRules 让选中节点自身的地址永远直连。
// TUN 会接管本机全部流量，包括"加速器自己去连节点"的那条连接；
// 不显式排除的话它会被再次送进隧道（环路风险 + 白耗节点带宽）。
func peerDirectRules(peers ...*config.Peer) []option.Rule {
	var cidrs []string
	seen := make(map[string]bool, len(peers))
	for _, p := range peers {
		if p == nil || p.Protocol == "direct" {
			continue
		}
		addr := strings.TrimSpace(p.Addr)
		if addr == "" || seen[addr] {
			continue
		}
		ip, err := netip.ParseAddr(addr)
		if err != nil {
			continue
		}
		seen[addr] = true
		if ip.Is4() {
			cidrs = append(cidrs, fmt.Sprintf("%s/32", ip.String()))
		} else {
			cidrs = append(cidrs, fmt.Sprintf("%s/128", ip.String()))
		}
	}
	rules := make([]option.Rule, 0, 2)
	if len(cidrs) > 0 {
		rules = append(rules, routeRule(option.RawDefaultRule{
			IPCIDR: badoption.Listable[string](cidrs),
		}, "direct"))
	}
	if domains := peerDomains(peers...); len(domains) > 0 {
		rules = append(rules, routeRule(option.RawDefaultRule{
			Domain: badoption.Listable[string](domains),
		}, "direct"))
	}
	return rules
}

// ExplainError 把 sing-box 的底层错误翻译成"用户能看懂、能行动"的中文提示，
// 同时保留原始错误文本，方便排查。
func ExplainError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case containsAny(msg, "access is denied", "operation not permitted", "permission denied", "administrator", "requires elevation", "wintun"):
		return withTip(err, "创建虚拟网卡需要管理员权限：请关闭后右键选择“以管理员身份运行”再试")
	case containsAny(msg, "raw.githubusercontent.com", "geosite", "geoip", "rule-set", "rule_set", "rule set"):
		return withTip(err, "规则集下载失败：首次启动需要能访问 GitHub 下载 geosite/geoip，请检查网络后重试（成功下载过一次后会走本地缓存）")
	case containsAny(msg, "only one usage of each socket address", "address already in use"):
		return withTip(err, "本地端口被占用（127.0.0.1:5123）：可能已经有一个 gpp 实例在运行，请先退出它再试")
	case containsAny(msg, "hysteria2", "handshake", "authentication"):
		return withTip(err, "节点握手/认证失败：请重新导入节点链接（账号或端口可能已变更），或换一个节点")
	case containsAny(msg, "no such host", "i/o timeout", "connection refused", "network is unreachable", "connection reset", "eof"):
		return withTip(err, "连接节点失败：请确认节点地址/端口可达、本机网络正常")
	default:
		return err
	}
}

func containsAny(s string, keys ...string) bool {
	for _, key := range keys {
		if strings.Contains(s, key) {
			return true
		}
	}
	return false
}

func withTip(err error, tip string) error {
	return fmt.Errorf("%s（原始错误：%v）", tip, err)
}
