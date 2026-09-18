// Package core 是 gpp 的唯一"核心"：持有配置、节点、TUN 隧道与全部运行时状态。
//
// GUI 与 TUI 都只是它的前端（见 backend/control），因此任何一端做的操作，
// 另一端看到的都是同一份状态，不会出现"两个界面各起一条隧道、互相覆盖配置"。
//
// 锁约定（避免死锁，务必遵守）：
//   - mu 是唯一的"状态锁"，保护 conf/选中节点/tunnel/告警/日志路径等全部状态；
//     慢操作（构造配置、启动隧道、探测延迟）一律不持锁进行。
//   - pingLock 只保护 Peer.Ping。加锁顺序恒为 mu -> pingLock，pingLock 绝不反向获取 mu。
//   - trafficLock / subsLock / warnLock 都是叶子锁，只在最内层短暂持有。
package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/danbai225/gpp/backend/client"
	"github.com/danbai225/gpp/backend/config"
	"github.com/danbai225/gpp/backend/data"
	"github.com/sagernet/sing-box/option"
)

// Tunnel 是"隧道"的最小接口，便于测试时替换（*box.Box 满足它）。
type Tunnel interface {
	Start() error
	Close() error
}

// NewTunnel 是隧道构造函数，测试可替换。
type NewTunnel func(game, httpPeer *config.Peer, proxyDNS, localDNS string, rules []option.Rule) (Tunnel, error)

// Event 是通过控制面广播给各前端的事件。
type Event struct {
	Type    string    `json:"type"` // started / stopped / warning / peers
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// Engine 持有全部运行时状态，方法均可被控制面并发调用。
type Engine struct {
	mu        sync.Mutex
	conf      *config.Config
	gamePeer  *config.Peer
	httpPeer  *config.Peer
	tunnel    Tunnel
	starting  bool
	newTunnel NewTunnel
	logOutput string
	kind      string
	warnings  []string

	pingLock sync.Mutex // 只保护 Peer.Ping

	trafficLock              sync.Mutex
	lastUp, lastDown         uint64
	lastAt                   time.Time
	upRate, downRate         uint64
	trafficSampleInitialized bool

	subsLock sync.Mutex
	subs     map[chan Event]struct{}
}

// New 创建核心实例（尚未读取配置）。
func New() *Engine {
	return &Engine{
		conf:      config.Default(),
		newTunnel: defaultNewTunnel,
		subs:      make(map[chan Event]struct{}),
	}
}

func defaultNewTunnel(game, httpPeer *config.Peer, proxyDNS, localDNS string, rules []option.Rule) (Tunnel, error) {
	return client.Client(game, httpPeer, proxyDNS, localDNS, rules)
}

// SetNewTunnel 替换隧道构造函数（仅测试使用）。
func (e *Engine) SetNewTunnel(fn NewTunnel) {
	if fn == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.newTunnel = fn
}

// SetLogOutput 设置 sing-box 日志文件路径（应在 Start 之前调用）。
func (e *Engine) SetLogOutput(path string) {
	e.mu.Lock()
	e.logOutput = path
	e.mu.Unlock()
	client.LogOutput = path
}

// LogOutput 返回当前日志文件路径。
func (e *Engine) LogOutput() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logOutput
}

// SetKind 记录核心由哪个前端启动（gui/tui）。
func (e *Engine) SetKind(kind string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.kind = kind
}

// Kind 返回核心来源。
func (e *Engine) Kind() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.kind
}

// Load 读取配置并校正选中节点。返回的 error 只是提示，配置一定可用。
func (e *Engine) Load() error {
	conf, err := config.LoadConfig()
	if conf != nil {
		e.mu.Lock()
		e.conf = conf
		e.mu.Unlock()
	}
	e.resolvePeers()
	e.emit(Event{Type: "peers", Message: "配置已加载"})
	return err
}

// ConfigPath 返回配置文件路径（供界面显示）。
func (e *Engine) ConfigPath() string { return config.Path() }

// Running 返回是否正在加速。
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tunnel != nil
}

// resolvePeers 校正选中节点并同步到 gamePeer/httpPeer（自行加锁）。
func (e *Engine) resolvePeers() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resolvePeersLocked()
}

// resolvePeersLocked 要求调用方已持有 e.mu。
func (e *Engine) resolvePeersLocked() {
	for _, warning := range e.conf.Normalize() {
		if strings.TrimSpace(warning) != "" {
			e.warnings = append(e.warnings, warning)
			message := warning
			go e.emit(Event{Type: "warning", Message: message})
		}
	}
	e.gamePeer = e.conf.FindPeer(e.conf.GamePeer)
	e.httpPeer = e.conf.FindPeer(e.conf.HTTPPeer)
	if e.httpPeer == nil {
		e.httpPeer = e.gamePeer
	}
}

// AddWarning 追加一条要展示给前端的提示。
func (e *Engine) AddWarning(warning string) {
	if strings.TrimSpace(warning) == "" {
		return
	}
	e.mu.Lock()
	e.warnings = append(e.warnings, warning)
	e.mu.Unlock()
	go e.emit(Event{Type: "warning", Message: warning})
}

// takeWarnings 取走累计告警（提示一次即清空）。
func (e *Engine) takeWarnings() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.warnings) == 0 {
		return ""
	}
	warning := strings.Join(e.warnings, "\n")
	e.warnings = nil
	return warning
}

// Subscribe 订阅事件；返回取消订阅函数。
func (e *Engine) Subscribe() (chan Event, func()) {
	ch := make(chan Event, 16)
	e.subsLock.Lock()
	e.subs[ch] = struct{}{}
	e.subsLock.Unlock()
	return ch, func() {
		e.subsLock.Lock()
		if _, ok := e.subs[ch]; ok {
			delete(e.subs, ch)
			close(ch)
		}
		e.subsLock.Unlock()
	}
}

func (e *Engine) emit(event Event) {
	if event.At.IsZero() {
		event.At = time.Now()
	}
	e.subsLock.Lock()
	defer e.subsLock.Unlock()
	for ch := range e.subs {
		select {
		case ch <- event:
		default: // 前端来不及消费就丢弃，绝不阻塞核心
		}
	}
}

// Status 返回给界面用的状态快照（含实时速率）。
func (e *Engine) Status() data.Status {
	e.mu.Lock()
	status := data.Status{
		Running:    e.tunnel != nil,
		GamePeer:   copyPeer(e.gamePeer),
		HttpPeer:   copyPeer(e.httpPeer),
		Warning:    strings.Join(e.warnings, "\n"),
		CoreKind:   e.kind,
		CorePID:    os.Getpid(),
		LogPath:    e.logOutput,
		ConfigPath: config.Path(),
		PeerCount:  len(e.conf.PeerList),
	}
	e.warnings = nil
	e.mu.Unlock()

	status.Up, status.Down = data.Traffic()
	status.UpRate, status.DownRate = e.updateRates(status.Up, status.Down)
	return status
}

// updateRates 用两次采样差值计算实时速率；网卡重建导致计数器回退时按 0 处理。
func (e *Engine) updateRates(up, down uint64) (upRate, downRate uint64) {
	e.trafficLock.Lock()
	defer e.trafficLock.Unlock()
	now := time.Now()
	if e.trafficSampleInitialized && up >= e.lastUp && down >= e.lastDown {
		if elapsed := now.Sub(e.lastAt).Seconds(); elapsed >= 0.2 {
			e.upRate = uint64(float64(up-e.lastUp) / elapsed)
			e.downRate = uint64(float64(down-e.lastDown) / elapsed)
		}
	}
	e.lastUp, e.lastDown, e.lastAt = up, down, now
	e.trafficSampleInitialized = true
	return e.upRate, e.downRate
}

// Peers 返回节点副本，顺序与配置一致（界面上的序号必须稳定，不能因为测速而跳动）。
func (e *Engine) Peers() []*config.Peer {
	return e.peersSnapshot(false)
}

// PeersByPing 返回按延迟排序的节点副本（未测速排最后），供"挑最快节点"的场景使用。
func (e *Engine) PeersByPing() []*config.Peer {
	return e.peersSnapshot(true)
}

func (e *Engine) peersSnapshot(sortByPing bool) []*config.Peer {
	e.mu.Lock()
	e.pingLock.Lock()
	list := make([]*config.Peer, 0, len(e.conf.PeerList))
	for _, peer := range e.conf.PeerList {
		if peer == nil {
			continue
		}
		list = append(list, copyPeer(peer))
	}
	e.pingLock.Unlock()
	e.mu.Unlock()

	if !sortByPing {
		return list
	}
	const unknownPing = ^uint(0)
	sort.SliceStable(list, func(i, j int) bool {
		pi, pj := list[i].Ping, list[j].Ping
		if pi == 0 {
			pi = unknownPing
		}
		if pj == 0 {
			pj = unknownPing
		}
		return pi < pj
	})
	return list
}

// Select 设置游戏/网页节点并落盘。
func (e *Engine) Select(game, httpPeer string) error {
	e.mu.Lock()
	changed := false
	if peer := e.conf.FindPeer(game); peer != nil {
		e.gamePeer = peer
		e.conf.GamePeer = peer.Name
		changed = true
	}
	if peer := e.conf.FindPeer(httpPeer); peer != nil {
		e.httpPeer = peer
		e.conf.HTTPPeer = peer.Name
		changed = true
	}
	if !changed {
		e.mu.Unlock()
		return errors.New("节点不存在，请刷新节点列表后重试")
	}
	running := e.tunnel != nil
	err := config.SaveConfig(e.conf)
	gameName, httpName := e.conf.GamePeer, e.conf.HTTPPeer
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if running {
		e.AddWarning("节点已切换，但当前隧道仍在使用旧节点：重启加速后生效")
	}
	e.emit(Event{Type: "peers", Message: fmt.Sprintf("已选择节点：%s / %s", gameName, httpName)})
	return nil
}

// Import 导入节点链接或订阅地址。
func (e *Engine) Import(token string) error {
	e.mu.Lock()
	err := config.AddPeer(e.conf, token)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.resolvePeers()
	e.emit(Event{Type: "peers", Message: "已导入节点"})
	return nil
}

// Delete 删除指定节点（并校正选中节点，不会留下悬空名字）。
func (e *Engine) Delete(name string) error {
	e.mu.Lock()
	err := config.DelPeer(e.conf, name)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.resolvePeers()
	e.emit(Event{Type: "peers", Message: "已删除节点：" + name})
	return nil
}

// PingAll 并发测速所有节点（跳过直连；加速中不测速以免干扰游戏流量）。
func (e *Engine) PingAll() {
	if e.Running() {
		return
	}
	e.mu.Lock()
	peers := make([]*config.Peer, 0, len(e.conf.PeerList))
	for _, peer := range e.conf.PeerList {
		if peer != nil && peer.Protocol != "direct" {
			peers = append(peers, peer)
		}
	}
	e.mu.Unlock()

	var group sync.WaitGroup
	for _, peer := range peers {
		group.Add(1)
		go func(p *config.Peer) {
			defer group.Done()
			ms := pingPort(p.Addr, p.Port)
			e.pingLock.Lock()
			p.Ping = ms
			e.pingLock.Unlock()
		}(peer)
	}
	group.Wait()
}

// Start 启动加速。慢操作（构造配置、建网卡、下载规则集）不持锁，
// 这样其他前端的状态查询不会被卡住。
func (e *Engine) Start() error {
	e.mu.Lock()
	if e.tunnel != nil {
		e.mu.Unlock()
		return nil // 已在加速，幂等
	}
	if e.starting {
		e.mu.Unlock()
		return errors.New("正在启动中，请稍候")
	}
	e.starting = true
	e.resolvePeersLocked()
	game, httpPeer := e.gamePeer, e.httpPeer
	proxyDNS, localDNS, rules := e.conf.ProxyDNS, e.conf.LocalDNS, e.conf.Rules
	newTunnel := e.newTunnel
	e.mu.Unlock()

	if game == nil {
		e.mu.Lock()
		e.starting = false
		e.mu.Unlock()
		return errors.New("还没有可用节点，请先导入节点")
	}

	tunnel, err := newTunnel(game, httpPeer, proxyDNS, localDNS, rules)
	if err == nil {
		err = tunnel.Start()
	}
	if err != nil {
		if tunnel != nil {
			_ = tunnel.Close()
		}
		e.mu.Lock()
		e.starting = false
		e.mu.Unlock()
		return client.ExplainError(err)
	}

	e.mu.Lock()
	e.starting = false
	e.tunnel = tunnel
	kind := e.kind
	e.mu.Unlock()
	e.emit(Event{Type: "started", Message: fmt.Sprintf("[%s] 加速已启动：%s", kind, game.Name)})
	return nil
}

// Stop 停止加速。
func (e *Engine) Stop() error {
	e.mu.Lock()
	tunnel := e.tunnel
	e.tunnel = nil
	e.mu.Unlock()
	if tunnel == nil {
		return nil
	}
	if err := tunnel.Close(); err != nil {
		return err
	}
	e.emit(Event{Type: "stopped", Message: "已停止加速"})
	return nil
}

// Restart 用当前节点重启隧道（换节点后生效）。
func (e *Engine) Restart() error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start()
}

// RunBackground 周期刷新节点延迟，直到 ctx 结束。
// 由"核心"进程调用一次：延迟是所有前端共享的状态，没必要每个前端各测一遍。
func (e *Engine) RunBackground(ctx context.Context) {
	e.PingAll()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.PingAll()
		}
	}
}

func copyPeer(p *config.Peer) *config.Peer {
	if p == nil {
		return nil
	}
	clone := *p
	return &clone
}

// pingPort 单次 TCP 探测延迟（毫秒）；失败返回 0。
func pingPort(host string, port uint16) uint {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 3*time.Second)
	if err != nil {
		return 0
	}
	_ = conn.Close()
	return uint(time.Since(start).Milliseconds())
}
