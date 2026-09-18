package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/option"
)

// TunInterfaceName 是 TUN 虚拟网卡名：配置、流量统计、诊断都引用它，避免各处硬编码漂移。
const TunInterfaceName = "utun225"

// Debug 全局调试开关：开启后 sing-box 以 trace 级别输出到 debug.log。
var Debug atomic.Bool

const (
	dirName      = ".gpp"
	configName   = "config.json"
	directName   = "直连"
	defaultProxy = "https://1.1.1.1/dns-query"
	defaultLocal = "https://223.5.5.5/dns-query"
)

var (
	pathMu     sync.RWMutex
	customPath string
)

type Peer struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     uint16 `json:"port"`
	Addr     string `json:"addr"`
	UUID     string `json:"uuid"`
	Ping     uint   `json:"ping"`
}

// Domain 返回节点域名：Addr 是 IP 时返回空串，空串不参与 DNS 规则匹配，
// 也就不会像以前那样往规则里塞 "placeholder.com" 这类假域名。nil 安全，避免漏判空导致 panic。
func (p *Peer) Domain() string {
	if p == nil {
		return ""
	}
	host := strings.TrimSpace(p.Addr)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.Trim(host, "[]") // IPv6 字面量带方括号
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		if _, err = netip.ParseAddr(h); err == nil {
			return ""
		}
		return h
	}
	return host
}

// Address 返回可读的 addr:port（直连节点返回 "-"）。
func (p *Peer) Address() string {
	if p == nil || p.Protocol == "direct" {
		return "-"
	}
	return fmt.Sprintf("%s:%d", p.Addr, p.Port)
}

type Config struct {
	PeerList []*Peer       `json:"peer_list"`
	SubAddr  string        `json:"sub_addr"`
	Rules    []option.Rule `json:"rules"`
	GamePeer string        `json:"game_peer"`
	HTTPPeer string        `json:"http_peer"`
	ProxyDNS string        `json:"proxy_dns"`
	LocalDNS string        `json:"local_dns"`
	Debug    bool          `json:"debug"`
}

// UserDir 返回 ~/.gpp 目录（不存在时创建）。
func UserDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	dir := filepath.Join(home, dirName)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// SetPath 显式指定配置文件路径（命令行参数、测试用）；传空串恢复默认解析。
func SetPath(path string) {
	pathMu.Lock()
	customPath = strings.TrimSpace(path)
	pathMu.Unlock()
}

// Path 返回配置文件路径。解析顺序与当前工作目录无关，
// 避免"换个目录启动就换了配置/日志"这类问题：
//  1. 可执行文件同级的 config.json（便携部署、老用户习惯）
//  2. 用户目录 ~/.gpp/config.json
func Path() string {
	pathMu.RLock()
	custom := customPath
	pathMu.RUnlock()
	if custom != "" {
		return custom
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		local := filepath.Join(filepath.Dir(exe), configName)
		if st, serr := os.Stat(local); serr == nil && !st.IsDir() {
			return local
		}
	}
	return filepath.Join(UserDir(), configName)
}

// SubCachePath 订阅缓存与配置文件同目录，便于整体迁移与测试隔离。
func SubCachePath() string {
	return filepath.Join(filepath.Dir(Path()), "sub_cache.json")
}

// Default 返回一份可直接使用的默认配置。
func Default() *Config {
	conf := &Config{
		PeerList: make([]*Peer, 0, 4),
		ProxyDNS: defaultProxy,
		LocalDNS: defaultLocal,
	}
	conf.Normalize()
	return conf
}

func directPeer() *Peer {
	return &Peer{Name: directName, Protocol: "direct", Port: 0, Addr: "127.0.0.1"}
}

// InitConfig 保证配置文件存在，返回的错误仅供日志使用。
func InitConfig() error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return SaveConfig(Default())
}

// LoadConfig 读取配置。
//
// 重要约定：**永远返回可用的配置**（首次运行、文件损坏、订阅失败都返回默认/部分配置），
// error 只表示"需要提示用户的告警"，调用方必须继续使用返回的配置，而不是当成致命错误丢掉。
func LoadConfig() (*Config, error) {
	path := Path()
	conf := Default()

	var warns []string
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		// Windows 记事本默认写 UTF-8 BOM，而 Go 的 json 解析器不接受 BOM。
		// 这里主动剥掉，避免"用记事本改过配置就打不开"这类莫名其妙的失败。
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
		if uerr := json.Unmarshal(data, conf); uerr != nil {
			// 配置损坏：备份原文件并重置，绝不返回 nil（旧版本会因此空指针崩溃）
			backup := fmt.Sprintf("%s.bad-%s", path, time.Now().Format("20060102150405"))
			if rerr := os.Rename(path, backup); rerr != nil {
				backup = path
			}
			warns = append(warns, fmt.Sprintf("配置文件损坏，已备份为 %s 并重置为默认配置：%v", filepath.Base(backup), uerr))
			conf = Default()
		}
	case errors.Is(err, os.ErrNotExist):
		// 首次运行：落盘一份默认配置，用户能立刻知道配置文件在哪
		if serr := SaveConfig(conf); serr != nil {
			warns = append(warns, fmt.Sprintf("创建默认配置失败：%v", serr))
		}
	default:
		warns = append(warns, fmt.Sprintf("读取配置失败（%s）：%v", path, err))
	}

	// 先合并订阅（不依赖选中状态），再校正默认值/去重/选中节点
	if subErr := conf.LoadSubscription(); subErr != nil {
		warns = append(warns, subErr.Error())
	}
	warns = append(warns, conf.Normalize()...)

	Debug.Store(conf.Debug)
	return conf, joinWarns(warns)
}

// SaveConfig 原子写入配置：先写临时文件再 rename，避免"写到一半崩溃"把配置写坏。
func SaveConfig(config *Config) error {
	if config == nil {
		return errors.New("配置为空")
	}
	if config.PeerList == nil {
		config.PeerList = make([]*Peer, 0)
	}
	data, err := json.MarshalIndent(config, "", " ")
	if err != nil {
		return err
	}
	path := Path()
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName) // rename 成功时这里是 no-op
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// FindPeer 按名字查找节点。
func (c *Config) FindPeer(name string) *Peer {
	if c == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	for _, p := range c.PeerList {
		if p != nil && p.Name == name {
			return p
		}
	}
	return nil
}

// Peers 返回节点列表副本（浅拷贝，节点指针共享），便于调用方加锁后安全遍历。
func (c *Config) Peers() []*Peer {
	if c == nil {
		return nil
	}
	list := make([]*Peer, len(c.PeerList))
	copy(list, c.PeerList)
	return list
}

// Normalize 修正默认值、去重节点、校正选中节点，返回给用户看的告警（可能为 nil）。
// 顺序保持稳定：原有节点保持原顺序，新增节点追加在末尾，直连节点恒定在末尾，
// 因此界面上"第 3 个节点"不会因为重启而变成别的节点。
func (c *Config) Normalize() []string {
	if c == nil {
		return nil
	}
	var warns []string
	if c.PeerList == nil {
		c.PeerList = make([]*Peer, 0)
	}
	if strings.TrimSpace(c.ProxyDNS) == "" {
		c.ProxyDNS = defaultProxy
	}
	if strings.TrimSpace(c.LocalDNS) == "" {
		c.LocalDNS = defaultLocal
	}

	seen := make(map[string]bool, len(c.PeerList)+1)
	peers := make([]*Peer, 0, len(c.PeerList)+1)
	for _, p := range c.PeerList {
		if p == nil {
			continue
		}
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			continue
		}
		if seen[p.Name] {
			warns = append(warns, fmt.Sprintf("节点 %s 重复，已忽略重复项", p.Name))
			continue
		}
		if p.Protocol == "" {
			p.Protocol = "vless"
		}
		seen[p.Name] = true
		peers = append(peers, p)
	}
	if !seen[directName] {
		peers = append(peers, directPeer())
	}
	c.PeerList = peers

	if len(c.PeerList) == 0 {
		c.GamePeer, c.HTTPPeer = "", ""
		return warns
	}

	if c.FindPeer(c.GamePeer) == nil {
		fallback := c.PeerList[0]
		for _, p := range c.PeerList {
			if p.Protocol != "direct" {
				fallback = p
				break
			}
		}
		if selected := strings.TrimSpace(c.GamePeer); selected != "" {
			warns = append(warns, fmt.Sprintf("已选游戏节点 %s 不存在（可能已删除或订阅已更新），已自动切换到 %s", selected, fallback.Name))
		}
		c.GamePeer = fallback.Name
	}
	if c.FindPeer(c.HTTPPeer) == nil {
		if selected := strings.TrimSpace(c.HTTPPeer); selected != "" {
			warns = append(warns, fmt.Sprintf("已选网页节点 %s 不存在，已自动切换为 %s", selected, c.GamePeer))
		}
		c.HTTPPeer = c.GamePeer
	}
	return warns
}

func joinWarns(warns []string) error {
	if len(warns) == 0 {
		return nil
	}
	return errors.New(strings.Join(warns, "\n"))
}

// decodeBase64 兼容标准/URL 安全、带/不带 padding 的 base64。
func decodeBase64(token string) ([]byte, error) {
	token = strings.TrimSpace(token)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(token)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// ParsePeer 解析 gpp:// 导入链接（base64 编码，可带 #名称 片段）。
func ParsePeer(token string) (*Peer, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("导入内容为空")
	}
	name := ""
	if idx := strings.LastIndex(token, "#"); idx >= 0 {
		name = strings.TrimSpace(token[idx+1:])
		token = strings.TrimSpace(token[:idx])
	}
	tokenBytes, err := decodeBase64(token)
	if err != nil {
		return nil, errors.New("导入链接不是合法的 base64，请确认内容复制完整")
	}
	plain := strings.TrimSpace(string(tokenBytes))
	protocolPart, rest, ok := strings.Cut(plain, "@")
	if !ok {
		return nil, fmt.Errorf("导入链接格式错误（缺少 @）：%s", plain)
	}
	protocol := strings.TrimPrefix(protocolPart, "gpp://")
	switch protocol {
	case "vless", "shadowsocks", "socks", "hysteria2":
	default:
		return nil, fmt.Errorf("不支持的协议：%s", protocol)
	}
	addrPart, uuid, ok := strings.Cut(rest, "/")
	if !ok || strings.TrimSpace(uuid) == "" {
		return nil, errors.New("导入链接格式错误（缺少 uuid）")
	}
	host, portStr, err := splitHostPort(addrPart)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("端口不合法：%s", portStr)
	}
	if name == "" {
		name = host + ":" + portStr
	}
	return &Peer{
		Name:     name,
		Protocol: protocol,
		Port:     uint16(port),
		Addr:     host,
		UUID:     strings.TrimSpace(uuid),
	}, nil
}

// splitHostPort 支持 IPv6 字面量 [::1]:443 与普通 host:port。
func splitHostPort(addr string) (host, port string, err error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", "", errors.New("地址为空")
	}
	if ap, perr := netip.ParseAddrPort(addr); perr == nil {
		return ap.Addr().String(), strconv.Itoa(int(ap.Port())), nil
	}
	idx := strings.LastIndex(addr, ":")
	if idx <= 0 || idx == len(addr)-1 {
		return "", "", fmt.Errorf("地址格式错误（应为 host:port）：%s", addr)
	}
	if strings.Contains(addr[:idx], ":") {
		return "", "", fmt.Errorf("IPv6 地址需要用方括号包裹，例如 [::1]:443：%s", addr)
	}
	return addr[:idx], addr[idx+1:], nil
}
