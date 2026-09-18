package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	subTimeout  = 15 * time.Second
	subMaxBytes = 4 << 20 // 订阅响应上限 4MB，避免恶意/异常地址把内存吃满
)

var subHTTPClient = &http.Client{Timeout: subTimeout}

// subCache 是最近一次成功拉取的订阅节点，订阅失败时用它兜底。
type subCache struct {
	UpdatedAt time.Time `json:"updated_at"`
	Peers     []*Peer   `json:"peers"`
}

// IsSubAddr 判断导入内容是否为订阅地址。
func IsSubAddr(token string) bool {
	lower := strings.ToLower(strings.TrimSpace(token))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// LoadSubscription 刷新订阅节点：成功则写本地缓存，失败则回退到本地缓存。
// 返回值 err 是**非致命告警**：订阅挂了不应该导致启动失败，调用方应继续使用返回的配置。
func (c *Config) LoadSubscription() error {
	if c == nil || strings.TrimSpace(c.SubAddr) == "" {
		return nil
	}
	peers, err := fetchSubscription(c.SubAddr)
	if err != nil {
		cached, cerr := readSubCache()
		if cerr != nil || len(cached.Peers) == 0 {
			return fmt.Errorf("订阅更新失败：%v（本地也没有可用缓存）", err)
		}
		c.mergePeers(cached.Peers)
		return fmt.Errorf("订阅更新失败：%v（已回退到 %s 的本地缓存，节点可能不是最新的）",
			err, cached.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	c.mergePeers(peers)
	if werr := writeSubCache(peers); werr != nil && Debug.Load() {
		fmt.Fprintln(os.Stderr, "写入订阅缓存失败:", werr)
	}
	return nil
}

// mergePeers 把订阅节点并入本地节点：同名以订阅为准（保留原位置，避免序号跳动），新节点追加在末尾。
func (c *Config) mergePeers(peers []*Peer) {
	index := make(map[string]int, len(c.PeerList))
	for i, p := range c.PeerList {
		if p != nil {
			index[p.Name] = i
		}
	}
	for _, remote := range peers {
		if remote == nil || strings.TrimSpace(remote.Name) == "" {
			continue
		}
		if i, ok := index[remote.Name]; ok {
			c.PeerList[i] = remote
			continue
		}
		index[remote.Name] = len(c.PeerList)
		c.PeerList = append(c.PeerList, remote)
	}
}

// AddPeer 导入一个 gpp:// 节点链接或订阅地址，成功后立即落盘。
func AddPeer(c *Config, token string) error {
	if c == nil {
		return errors.New("配置未初始化")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("导入内容为空")
	}
	if IsSubAddr(token) {
		peers, err := fetchSubscription(token)
		if err != nil {
			return err
		}
		c.SubAddr = token
		c.mergePeers(peers)
		c.Normalize()
		c.preferImportedPeer()
		if err = SaveConfig(c); err != nil {
			return err
		}
		_ = writeSubCache(peers)
		return nil
	}
	peer, err := ParsePeer(token)
	if err != nil {
		return err
	}
	if c.FindPeer(peer.Name) != nil {
		return fmt.Errorf("节点 %s 已存在", peer.Name)
	}
	c.PeerList = append(c.PeerList, peer)
	c.Normalize()
	c.preferImportedPeer()
	return SaveConfig(c)
}

// preferImportedPeer 在"当前选的是直连（或还没选）"时自动切到刚导入的真实节点：
// 少一次手动选择，也避免用户以为在加速其实走的是直连。
func (c *Config) preferImportedPeer() {
	if c.GamePeer != "" && c.GamePeer != directName {
		return
	}
	for _, p := range c.PeerList {
		if p == nil || p.Protocol == "direct" {
			continue
		}
		c.GamePeer = p.Name
		if c.HTTPPeer == "" || c.HTTPPeer == directName {
			c.HTTPPeer = p.Name
		}
		return
	}
}

// DelPeer 删除节点并校正选中节点（删除当前正在使用的节点后不会留下悬空名字）。
func DelPeer(c *Config, name string) error {
	if c == nil {
		return errors.New("配置未初始化")
	}
	name = strings.TrimSpace(name)
	found := false
	peers := make([]*Peer, 0, len(c.PeerList))
	for _, p := range c.PeerList {
		if p != nil && p.Name == name {
			found = true
			continue
		}
		peers = append(peers, p)
	}
	if !found {
		return fmt.Errorf("节点 %s 不存在", name)
	}
	c.PeerList = peers
	c.Normalize()
	return SaveConfig(c)
}

// fetchSubscription 拉取并校验订阅内容。
func fetchSubscription(addr string) ([]*Peer, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimSpace(addr), nil)
	if err != nil {
		return nil, fmt.Errorf("订阅地址不合法：%v", err)
	}
	req.Header.Set("User-Agent", "gpp")
	resp, err := subHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("订阅请求失败：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("订阅返回状态码 %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, subMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("读取订阅内容失败：%v", err)
	}
	return ParseSubscription(data)
}

// ParseSubscription 解析订阅响应，兼容两种格式：
//  1. JSON 数组：[{"name":"hk","protocol":"vless","addr":"1.2.3.4","port":443,"uuid":"..."}]
//  2. base64 编码的 JSON 数组（部分服务端/机场订阅的常见做法）
//
// 会丢弃无效节点：避免一条坏数据让整个订阅不可用，或往配置里注入"直连"节点。
func ParseSubscription(data []byte) ([]*Peer, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("订阅内容为空")
	}
	if trimmed[0] == '[' {
		return decodePeerList(trimmed)
	}
	if decoded, err := decodeBase64(string(trimmed)); err == nil {
		if inner := bytes.TrimSpace(decoded); len(inner) > 0 && inner[0] == '[' {
			if peers, derr := decodePeerList(inner); derr == nil {
				return peers, nil
			}
		}
	}
	return nil, errors.New("订阅内容不是 gpp 节点列表（既不是 JSON 数组，也不是 base64 编码的 JSON 数组）")
}

func decodePeerList(data []byte) ([]*Peer, error) {
	var raw []*Peer
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("订阅节点解析失败：%v", err)
	}
	peers := make([]*Peer, 0, len(raw))
	for _, p := range raw {
		if p == nil {
			continue
		}
		p.Name = strings.TrimSpace(p.Name)
		p.Addr = strings.TrimSpace(p.Addr)
		if p.Name == "" || p.Addr == "" {
			continue
		}
		if p.Protocol == "direct" || p.Name == directName {
			continue // 订阅无权注入直连节点
		}
		if p.Protocol == "" {
			p.Protocol = "vless"
		}
		if p.Protocol != "direct" && p.Port == 0 {
			continue
		}
		p.Ping = 0
		peers = append(peers, p)
	}
	if len(peers) == 0 {
		return nil, errors.New("订阅里没有有效的 gpp 节点")
	}
	return peers, nil
}

func readSubCache() (*subCache, error) {
	data, err := os.ReadFile(SubCachePath())
	if err != nil {
		return nil, err
	}
	cache := &subCache{}
	if err = json.Unmarshal(data, cache); err != nil {
		return nil, err
	}
	if len(cache.Peers) == 0 {
		return nil, errors.New("缓存为空")
	}
	return cache, nil
}

func writeSubCache(peers []*Peer) error {
	cache := &subCache{UpdatedAt: time.Now(), Peers: peers}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	path := SubCachePath()
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
