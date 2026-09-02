package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/google/uuid"
	box "github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

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
			Options: &option.DirectOutboundOptions{},
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

func Client(gamePeer, httpPeer *config.Peer, proxyDNS, localDNS string, rules []option.Rule) (*box.Box, error) {
	proxyOut := getOUt(gamePeer)
	httpOut := proxyOut
	if httpPeer != nil {
		httpOut = getOUt(httpPeer)
	}
	httpOut.Tag = "http"
	proxyOut.Tag = "proxy"

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
			Log: &option.LogOptions{
				Disabled: true,
			},
			DNS: &option.DNSOptions{
				RawDNSOptions: option.RawDNSOptions{
					Servers: []option.DNSServerOptions{
						proxyDNSServer,
						localDNSServer,
					},
					Rules: []option.DNSRule{
						dnsRouteRule(option.RawDefaultDNSRule{
							Domain: badoption.Listable[string]{
								gamePeer.Domain(),
								httpPeer.Domain(),
							},
						}, "localDns"),
						dnsRouteRule(option.RawDefaultDNSRule{
							RuleSet: badoption.Listable[string]{"geosite-cn"},
						}, "localDns"),
						dnsRouteRule(option.RawDefaultDNSRule{
							RuleSet: badoption.Listable[string]{"geosite-geolocation-!cn"},
						}, "proxyDns"),
					},
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
					Options: &option.DirectOutboundOptions{},
				},
			},
		},
	}

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
			Output:       "debug.log",
			Timestamp:    true,
			DisableColor: true,
		}
		content, err := options.Options.MarshalJSONContext(options.Context)
		if err == nil {
			var buf bytes.Buffer
			if json.Indent(&buf, content, "", " ") == nil {
				_ = os.WriteFile("sing.json", buf.Bytes(), 0o644)
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
