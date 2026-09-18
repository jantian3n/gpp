// gpp-tui: gpp 加速器的终端客户端，带完整日志与实时状态，便于排查问题。
//
// 用法: 以管理员身份运行 gpp-tui.exe（TUN 需要管理员权限）
// 日志: 用户目录 ~/.gpp/gpp-tui.log（sing-box 运行日志），debug 开启后另有 debug.log
// 配置: 与 GUI 共用同一份 config.json（可执行文件同级，或 ~/.gpp/config.json）
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/danbai225/gpp/backend/client"
	"github.com/danbai225/gpp/backend/config"
	"github.com/danbai225/gpp/backend/data"
	box "github.com/sagernet/sing-box"
)

var (
	conf     *config.Config
	gamePeer *config.Peer
	httpPeer *config.Peer
	instance *box.Box

	mu    sync.Mutex // 保护上面 4 个变量以及 Peer.Ping
	state = struct {
		lastUp, lastDown uint64
		lastAt           time.Time
		useANSI          bool
		watch            bool
	}{}

	cleanupOnce sync.Once
)

func recordPanic(where string) {
	if r := recover(); r != nil {
		path := filepath.Join(config.UserDir(), "panic.log")
		_ = os.WriteFile(path,
			[]byte(fmt.Sprintf("[%s] %s panic: %v\n%s\n", time.Now().Format(time.RFC3339), where, r, debug.Stack())),
			0o644)
	}
}

// snapshot 在锁内取出一份状态副本，避免与后台测速/交互命令互相踩踏。
func snapshot() (running bool, game, http *config.Peer, peers []*config.Peer) {
	mu.Lock()
	defer mu.Unlock()
	clone := func(p *config.Peer) *config.Peer {
		if p == nil {
			return nil
		}
		c := *p
		return &c
	}
	peers = make([]*config.Peer, 0, len(conf.PeerList))
	for _, p := range conf.PeerList {
		peers = append(peers, clone(p))
	}
	return instance != nil, clone(gamePeer), clone(httpPeer), peers
}

func nameOf(p *config.Peer) string {
	if p == nil {
		return "未选择"
	}
	return p.Name
}

func humanBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func pingText(p *config.Peer) string {
	switch {
	case p == nil:
		return "-"
	case p.Protocol == "direct":
		return "-"
	case p.Ping == 0:
		return "未测速"
	default:
		return strconv.FormatUint(uint64(p.Ping), 10) + "ms"
	}
}

// trafficText 返回"累计 + 实时速率"。网卡重建后计数器会归零，这里做下溢保护。
func trafficText() string {
	up, down := data.Traffic()
	now := time.Now()
	text := fmt.Sprintf("流量 ↑%s ↓%s", humanBytes(up), humanBytes(down))

	mu.Lock()
	lastUp, lastDown, lastAt := state.lastUp, state.lastDown, state.lastAt
	state.lastUp, state.lastDown, state.lastAt = up, down, now
	mu.Unlock()

	if !lastAt.IsZero() && up >= lastUp && down >= lastDown {
		if elapsed := now.Sub(lastAt).Seconds(); elapsed >= 0.2 {
			text += fmt.Sprintf(" | ↑%s/s ↓%s/s",
				humanBytes(uint64(float64(up-lastUp)/elapsed)),
				humanBytes(uint64(float64(down-lastDown)/elapsed)))
		}
	}
	return text
}

func draw() {
	running, game, http, peers := snapshot()
	if state.useANSI {
		fmt.Print("\x1b[2J\x1b[H") // 清屏 + 光标归位
	}
	fmt.Println("================ gpp 加速器 (TUI) ================")
	fmt.Printf("配置: %s\n", config.Path())
	fmt.Printf("日志: %s | debug: %v\n", client.LogOutput, config.Debug.Load())
	fmt.Println("--------------------------------------------------")
	if len(peers) == 0 {
		fmt.Println(" (还没有节点，输入 i <导入链接> 添加)")
	}
	for i, p := range peers {
		var tags []string
		if game != nil && p.Name == game.Name {
			tags = append(tags, "Game")
		}
		if http != nil && p.Name == http.Name {
			tags = append(tags, "Http")
		}
		fmt.Printf(" %2d  %-14s %-11s %-22s %-8s %s\n",
			i+1, p.Name, p.Protocol, p.Address(), pingText(p), strings.Join(tags, "+"))
	}
	fmt.Println("--------------------------------------------------")
	status := "未加速"
	if running {
		status = "加速中"
	}
	fmt.Printf("状态: %s | Game: %s | Http: %s\n", status, nameOf(game), nameOf(http))
	fmt.Printf("%s\n", trafficText())
	fmt.Println("命令: 数字=Game | h数字=Http | s=开始 | t=停止 | r=重启 | p=测速 | a=自动选最快 | i=导入 | x=删除 | w=实时监控 | d=debug | ?=帮助 | q=退出")
}

func helptext() {
	fmt.Println(`命令说明:
  1..n       选择第 n 个节点作为 Game(游戏)节点
  h1..hn     选择第 n 个节点作为 Http(网页/下载)节点
  s / t      开始 / 停止加速
  r          重启加速（更换节点后用它生效）
  p          测速：逐个节点做 TCP 探测（hysteria2 只监听 UDP，结果仅供参考）
  a          测速后自动选择延迟最低的节点作为 Game 节点
  i <内容>   导入：gpp:// 的 base64 链接，或订阅地址
  x <序号>   删除第 n 个节点
  w          实时监控：每秒刷新状态，按回车返回
  d          切换 debug 日志（trace 级别，下次启动加速生效）
  ?          显示本帮助
  q          退出（会先停止加速）`)
}

func syncPeers() {
	mu.Lock()
	defer mu.Unlock()
	conf.Normalize()
	gamePeer = conf.FindPeer(conf.GamePeer)
	httpPeer = conf.FindPeer(conf.HTTPPeer)
	if httpPeer == nil {
		httpPeer = gamePeer
	}
}

func saveConfig() {
	mu.Lock()
	defer mu.Unlock()
	if err := config.SaveConfig(conf); err != nil {
		fmt.Println("!! 保存配置失败:", err)
	}
}

func selectPeer(n int, isHTTP bool) {
	mu.Lock()
	defer mu.Unlock()
	if n < 1 || n > len(conf.PeerList) {
		fmt.Println("!! 序号超出范围（当前共", len(conf.PeerList), "个节点）")
		return
	}
	p := conf.PeerList[n-1]
	if isHTTP {
		httpPeer = p
		conf.HTTPPeer = p.Name
		fmt.Println("Http(网页)节点 ->", p.Name)
	} else {
		gamePeer = p
		conf.GamePeer = p.Name
		fmt.Println("Game(游戏)节点 ->", p.Name)
	}
	if instance != nil {
		fmt.Println("!! 加速仍在用旧节点，输入 r 重启加速后生效")
	}
	if err := config.SaveConfig(conf); err != nil {
		fmt.Println("!! 保存配置失败:", err)
	}
}

func startBox() {
	mu.Lock()
	if instance != nil {
		mu.Unlock()
		fmt.Println("已在加速中（t 停止，r 重启）")
		return
	}
	game, http := gamePeer, httpPeer
	proxyDNS, localDNS, rules := conf.ProxyDNS, conf.LocalDNS, conf.Rules
	mu.Unlock()

	if game == nil {
		fmt.Println("!! 请先选择 Game 节点（或输入 i 导入节点）")
		return
	}
	if !isAdmin() {
		fmt.Println("!! 当前不是管理员身份：创建 TUN 虚拟网卡会失败，请右键“以管理员身份运行”")
		return
	}
	fmt.Println("正在启动加速（首次运行需要下载 geosite/geoip 规则集，可能需要十几秒）...")
	b, err := client.Client(game, http, proxyDNS, localDNS, rules)
	if err != nil {
		fmt.Println("!! 构建失败:", client.ExplainError(err))
		return
	}
	if err = b.Start(); err != nil {
		_ = b.Close()
		fmt.Println("!! 加速失败:", client.ExplainError(err))
		return
	}
	mu.Lock()
	instance = b
	mu.Unlock()
	fmt.Println("加速已启动")
}

// stopBox 停止加速并释放 TUN。返回是否真的停了。
func stopBox(quiet bool) bool {
	mu.Lock()
	inst := instance
	instance = nil
	mu.Unlock()
	if inst == nil {
		if !quiet {
			fmt.Println("未在加速")
		}
		return false
	}
	if err := inst.Close(); err != nil {
		fmt.Println("!! 停止失败:", err)
		return false
	}
	if !quiet {
		fmt.Println("已停止加速")
	}
	return true
}

// cleanup 保证进程退出前释放 TUN（Ctrl+C、q、EOF 都走这里）。
func cleanup() {
	cleanupOnce.Do(func() { stopBox(true) })
}

type pingResult struct {
	name string
	ms   uint
	note string
}

// pingAll 并发探测所有节点。interactive=false 时只更新数据不打印（用于启动时的静默测速）。
func pingAll(interactive bool) []string {
	_, _, _, peers := snapshot()
	results := make([]pingResult, 0, len(peers))
	var wg sync.WaitGroup
	var resMu sync.Mutex

	for _, p := range peers {
		if p == nil || p.Protocol == "direct" {
			continue
		}
		wg.Add(1)
		go func(p *config.Peer) {
			defer wg.Done()
			defer recordPanic("pingAll")
			ms, note := pingPeer(p)
			mu.Lock()
			p.Ping = ms
			mu.Unlock()
			resMu.Lock()
			results = append(results, pingResult{name: p.Name, ms: ms, note: note})
			resMu.Unlock()
		}(p)
	}
	wg.Wait()

	if !interactive {
		return nil
	}
	sort.SliceStable(results, func(i, j int) bool {
		ri, rj := results[i].ms, results[j].ms
		if ri == 0 {
			ri = ^uint(0)
		}
		if rj == 0 {
			rj = ^uint(0)
		}
		return ri < rj
	})
	lines := make([]string, 0, len(results)+1)
	for _, r := range results {
		switch {
		case r.ms == 0:
			lines = append(lines, fmt.Sprintf("  %-14s 不可达 %s", r.name, r.note))
		default:
			lines = append(lines, fmt.Sprintf("  %-14s %dms %s", r.name, r.ms, r.note))
		}
	}
	return lines
}

// pingPeer 返回节点延迟。hysteria2 只监听 UDP，TCP 被拒说明主机在线，它的耗时只作为参考。
func pingPeer(p *config.Peer) (uint, string) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", p.Addr, p.Port), 3*time.Second)
	if err == nil {
		_ = conn.Close()
		return uint(time.Since(start).Milliseconds()), ""
	}
	var errno syscall.Errno
	refused := errors.Is(err, syscall.ECONNREFUSED) ||
		(errors.As(err, &errno) && errno == syscall.ECONNREFUSED) ||
		strings.Contains(strings.ToLower(err.Error()), "refused")
	if p.Protocol == "hysteria2" && refused {
		return uint(time.Since(start).Milliseconds()), "(UDP 在线，仅供参考)"
	}
	return 0, fmt.Sprintf("(%v)", err)
}

// chooseFastest 测速并自动把最快的节点选为 Game 节点。
func chooseFastest() {
	fmt.Println("正在测速...")
	pingAll(true)
	_, _, _, peers := snapshot()
	best := (*config.Peer)(nil)
	for _, p := range peers {
		if p == nil || p.Protocol == "direct" || p.Ping == 0 {
			continue
		}
		if best == nil || p.Ping < best.Ping {
			best = p
		}
	}
	if best == nil {
		fmt.Println("!! 没有测到可用节点，请检查网络或节点地址")
		return
	}
	mu.Lock()
	conf.GamePeer = best.Name
	gamePeer = best
	mu.Unlock()
	fmt.Printf("已自动选择最快节点: %s (%dms)\n", best.Name, best.Ping)
	if err := config.SaveConfig(conf); err != nil {
		fmt.Println("!! 保存配置失败:", err)
	}
	mu.Lock()
	running := instance != nil
	mu.Unlock()
	if running {
		fmt.Println("!! 加速仍在用旧节点，输入 r 重启加速后生效")
	}
}

func importPeer(token string) {
	mu.Lock()
	cfg := conf
	mu.Unlock()
	if strings.TrimSpace(token) == "" {
		fmt.Println("用法: i <gpp:// 的 base64 链接 或 订阅地址>")
		return
	}
	if err := config.AddPeer(cfg, token); err != nil {
		fmt.Println("!! 导入失败:", err)
		return
	}
	syncPeers()
	fmt.Println("导入成功")
}

func deletePeer(n int) {
	mu.Lock()
	if n < 1 || n > len(conf.PeerList) {
		mu.Unlock()
		fmt.Println("!! 序号超出范围")
		return
	}
	name := conf.PeerList[n-1].Name
	mu.Unlock()

	if err := config.DelPeer(conf, name); err != nil {
		fmt.Println("!! 删除失败:", err)
		return
	}
	syncPeers()
	fmt.Println("已删除节点:", name)
	mu.Lock()
	running := instance != nil
	mu.Unlock()
	if running {
		fmt.Println("!! 加速仍在用旧配置，输入 r 重启加速后生效")
	}
}

// handle 处理一条命令，返回 (是否退出, 是否进入实时监控)。
func handle(cmd string) (quit, watch bool) {
	switch {
	case cmd == "q":
		cleanup()
		fmt.Println("bye")
		return true, false
	case cmd == "s":
		startBox()
	case cmd == "t":
		stopBox(false)
	case cmd == "r":
		if stopBox(true) {
			fmt.Println("已停止，正在用当前节点重启...")
		}
		startBox()
	case cmd == "p":
		fmt.Println("正在测速（最多 3 秒/节点）...")
		for _, line := range pingAll(true) {
			fmt.Println(line)
		}
	case cmd == "a":
		chooseFastest()
	case cmd == "w":
		return false, true
	case cmd == "d":
		config.Debug.Store(!config.Debug.Load())
		fmt.Println("debug =", config.Debug.Load(), "(仅本次会话生效，启动加速时写入 debug.log)")
	case cmd == "?":
		helptext()
	case strings.HasPrefix(cmd, "h"):
		n, err := strconv.Atoi(strings.TrimPrefix(cmd, "h"))
		if err != nil {
			fmt.Println("!! 无法识别命令:", cmd)
			return false, false
		}
		selectPeer(n, true)
	case strings.HasPrefix(cmd, "i"):
		importPeer(strings.TrimSpace(strings.TrimPrefix(cmd, "i")))
	case strings.HasPrefix(cmd, "x"):
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(cmd, "x")))
		if err != nil {
			fmt.Println("用法: x <序号>")
			return false, false
		}
		deletePeer(n)
	default:
		n, err := strconv.Atoi(cmd)
		if err != nil {
			fmt.Println("!! 无法识别命令:", cmd, "（? 查看帮助）")
			return false, false
		}
		selectPeer(n, false)
	}
	return false, false
}

// inputLoop 把标准输入拆成一行一条命令；输入结束（EOF/Ctrl+Z）时关闭通道。
func inputLoop() <-chan string {
	lines := make(chan string)
	go func() {
		defer close(lines)
		defer recordPanic("inputLoop")
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if strings.TrimSpace(line) != "" {
					lines <- line
				}
				return
			}
			lines <- line
		}
	}()
	return lines
}

func main() {
	configPath := flag.String("config", "", "指定配置文件路径（默认：可执行文件同级或 ~/.gpp/config.json）")
	noPing := flag.Bool("no-ping", false, "启动时不自动测速")
	flag.Parse()
	if *configPath != "" {
		config.SetPath(*configPath)
	}

	client.LogOutput = filepath.Join(config.UserDir(), "gpp-tui.log")
	state.useANSI = enableANSI()

	loaded, err := config.LoadConfig()
	if loaded == nil { // LoadConfig 保证不返回 nil，这里是最后一道保险
		loaded = config.Default()
	}
	conf = loaded
	if err != nil {
		fmt.Println("!! 配置提示:", err)
	}
	syncPeers()

	fmt.Println("gpp-tui 启动")
	fmt.Println("配置:", config.Path())
	fmt.Println("sing-box 日志:", client.LogOutput)
	if !isAdmin() {
		fmt.Println("!! 当前不是管理员身份，加速时会创建虚拟网卡失败，请以管理员身份重新运行")
	}
	if !*noPing {
		go func() {
			defer recordPanic("startupPing")
			pingAll(false)
		}()
	}

	// Ctrl+C / 终止信号：先释放 TUN 再退出，避免留下虚拟网卡和路由残留
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n收到退出信号，正在停止加速...")
		cleanup()
		os.Exit(0)
	}()

	lines := inputLoop()
	var ticker *time.Ticker
	for {
		if !state.watch {
			draw()
			fmt.Print("> ")
		}
		var tickCh <-chan time.Time
		if state.watch {
			if ticker == nil {
				ticker = time.NewTicker(time.Second)
				defer ticker.Stop()
			}
			tickCh = ticker.C
		}
		select {
		case line, ok := <-lines:
			if !ok {
				cleanup()
				fmt.Println("输入已结束，退出")
				return
			}
			cmd := strings.TrimSpace(line)
			if cmd == "" {
				// 实时监控模式下按回车返回命令模式
				state.watch = false
				continue
			}
			quit, watch := func() (quit, watch bool) {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("!! 命令执行异常:", r)
					}
				}()
				return handle(cmd)
			}()
			if quit {
				return
			}
			state.watch = watch
			if state.watch {
				fmt.Println("实时监控中：每秒刷新，按回车返回命令模式")
			}
		case <-tickCh:
			draw()
		}
	}
}
