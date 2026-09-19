// gpp-tui: gpp 加速器的终端前端。
//
// 它不直接创建隧道：真正的隧道由"核心"进程持有（谁先启动谁当核心），
// 本程序通过本机控制面（127.0.0.1，见 backend/control）读写同一份状态，
// 因此可以和 GUI 同时打开，两边显示与操作完全一致。
//
// 用法: gpp-tui.exe [-config 路径]
// 日志: 由核心写入 ~/.gpp/gpp-tui.log（本进程是核心时生效）
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/danbai225/gpp/backend/control"
	"github.com/danbai225/gpp/backend/data"
)

var (
	handle *control.Handle

	state = struct {
		useANSI     bool
		watch       bool
		confirmQuit bool
		lastError   string
	}{}

	cleanupOnce sync.Once
)

// version 由构建时 -ldflags "-X main.version=..." 注入（CI 注入 tag 号）。
var version = "dev"

func recordPanic(where string) {
	if r := recover(); r != nil {
		_ = os.WriteFile(filepath.Join(config.UserDir(), "panic.log"),
			[]byte(fmt.Sprintf("[%s] %s panic: %v\n%s\n", time.Now().Format(time.RFC3339), where, r, debug.Stack())),
			0o644)
	}
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

func nameOf(p *config.Peer) string {
	if p == nil {
		return "未选择"
	}
	return p.Name
}

// snapshot 读取控制面状态；失败时返回一个带告警的空状态，界面不至于空白。
func snapshot() (*data.Status, []*config.Peer) {
	status, err := handle.Status()
	if err != nil {
		return &data.Status{Warning: "与核心失去连接：" + err.Error()}, nil
	}
	peers, err := handle.Peers()
	if err != nil {
		return status, nil
	}
	return status, peers
}

func draw() {
	status, peers := snapshot()
	if state.useANSI {
		fmt.Print("\x1b[2J\x1b[H")
	}
	fmt.Println("================ gpp 加速器 (TUI) ================")
	role := fmt.Sprintf("本进程是核心（PID %d）", handle.Info().PID)
	if !handle.IsOwner() {
		role = fmt.Sprintf("已连接到核心 PID %d（来自 %s）", handle.Info().PID, handle.Info().Kind)
	}
	fmt.Printf("控制面: %s\n", role)
	fmt.Printf("配置: %s\n", status.ConfigPath)
	fmt.Printf("日志: %s\n", status.LogPath)
	fmt.Println("--------------------------------------------------")
	if len(peers) == 0 {
		fmt.Println(" (还没有节点，输入 i <导入链接> 添加)")
	}
	for i, p := range peers {
		var tags []string
		if status.GamePeer != nil && p.Name == status.GamePeer.Name {
			tags = append(tags, "Game")
		}
		if status.HttpPeer != nil && p.Name == status.HttpPeer.Name {
			tags = append(tags, "Http")
		}
		fmt.Printf(" %2d  %-14s %-11s %-22s %-8s %s\n",
			i+1, p.Name, p.Protocol, p.Address(), pingText(p), strings.Join(tags, "+"))
	}
	fmt.Println("--------------------------------------------------")
	runState := "未加速"
	if status.Running {
		runState = "加速中"
	}
	fmt.Printf("状态: %s | Game: %s | Http: %s\n", runState, nameOf(status.GamePeer), nameOf(status.HttpPeer))
	fmt.Printf("流量 累计 ↑%s ↓%s | 实时 ↑%s/s ↓%s/s\n",
		humanBytes(status.Up), humanBytes(status.Down),
		humanBytes(status.UpRate), humanBytes(status.DownRate))
	if status.Warning != "" {
		fmt.Println("提示:", status.Warning)
	}
	if state.lastError != "" {
		fmt.Println("!!", state.lastError)
	}
	fmt.Println("命令: 数字=Game | h数字=Http | s=开始 | t=停止 | r=重启 | p=测速 | a=自动选最快 | i=导入 | x=删除 | w=实时监控 | d=debug | ?=帮助 | q=退出")
}

func helptext() {
	fmt.Println(`命令说明:
  1..n       选择第 n 个节点作为 Game(游戏)节点
  h1..hn     选择第 n 个节点作为 Http(网页/下载)节点
  s / t      开始 / 停止加速（通过控制面，隧道由核心持有）
  r          重启加速（更换节点后用它生效）
  p          立即测速一次（核心也会每 5 秒自动刷新延迟）
  a          测速后自动选择延迟最低的节点作为 Game 节点
  i <内容>   导入：gpp:// 的 base64 链接，或订阅地址
  x <序号>   删除第 n 个节点
  w          实时监控：每秒刷新状态，按回车返回
  d          切换 debug 日志（trace 级别，下次启动加速生效）
  ?          显示本帮助
  q          退出（本进程若是核心会先停止加速；若只是前端，加速会继续）`)
}

func setError(err error) {
	if err == nil {
		state.lastError = ""
		return
	}
	state.lastError = err.Error()
}

func runCommand(fn func() error) {
	setError(fn())
}

func startTunnel() {
	status, _ := snapshot()
	if status.Running {
		fmt.Println("已在加速中（t 停止，r 重启）")
		return
	}
	if status.GamePeer == nil {
		fmt.Println("!! 请先选择 Game 节点（或输入 i 导入节点）")
		return
	}
	if handle.IsOwner() && !isAdmin() {
		fmt.Println("!! 本进程是核心，创建 TUN 虚拟网卡需要管理员权限：请以管理员身份重新运行")
		return
	}
	fmt.Println("正在启动加速（首次运行需要下载 geosite/geoip 规则集，可能需要十几秒）...")
	if err := handle.Start(); err != nil {
		fmt.Println("!! 加速失败:", err)
		return
	}
	fmt.Println("加速已启动")
}

func stopTunnel(quiet bool) {
	if err := handle.Stop(); err != nil {
		fmt.Println("!! 停止失败:", err)
		return
	}
	if !quiet {
		fmt.Println("已停止加速")
	}
}

func selectPeer(n int, isHTTP bool) {
	_, peers := snapshot()
	if n < 1 || n > len(peers) {
		fmt.Println("!! 序号超出范围（当前共", len(peers), "个节点）")
		return
	}
	target := peers[n-1]
	status, _ := snapshot()
	game, httpPeer := nameOf(status.GamePeer), nameOf(status.HttpPeer)
	if isHTTP {
		httpPeer = target.Name
	} else {
		game = target.Name
	}
	if err := handle.Select(game, httpPeer); err != nil {
		fmt.Println("!! 保存失败:", err)
		return
	}
	if isHTTP {
		fmt.Println("Http(网页)节点 ->", target.Name)
	} else {
		fmt.Println("Game(游戏)节点 ->", target.Name)
	}
}

func importPeer(token string) {
	if strings.TrimSpace(token) == "" {
		fmt.Println("用法: i <gpp:// 的 base64 链接 或 订阅地址>")
		return
	}
	if err := handle.Import(strings.TrimSpace(token)); err != nil {
		fmt.Println("!! 导入失败:", err)
		return
	}
	fmt.Println("导入成功")
}

func deletePeer(n int) {
	_, peers := snapshot()
	if n < 1 || n > len(peers) {
		fmt.Println("!! 序号超出范围")
		return
	}
	name := peers[n-1].Name
	if err := handle.Delete(name); err != nil {
		fmt.Println("!! 删除失败:", err)
		return
	}
	fmt.Println("已删除节点:", name)
}

// chooseFastest 让核心测速后自动选最快节点（并提示是否需要重启隧道）。
func chooseFastest() {
	fmt.Println("正在测速...")
	if err := handle.PingAll(); err != nil {
		fmt.Println("!! 测速失败:", err)
		return
	}
	for i := 0; i < 40; i++ { // 最多等 4 秒，等核心把结果写回
		time.Sleep(100 * time.Millisecond)
		if peers, err := handle.PeersByPing(); err == nil && len(peers) > 0 && peers[0].Ping > 0 {
			break
		}
	}
	peers, err := handle.PeersByPing()
	if err != nil {
		fmt.Println("!! 读取节点失败:", err)
		return
	}
	var best *config.Peer
	for _, peer := range peers {
		if peer.Protocol == "direct" || peer.Ping == 0 {
			continue
		}
		best = peer
		break
	}
	if best == nil {
		fmt.Println("!! 没有测到可用节点，请检查网络或节点地址")
		return
	}
	status, _ := snapshot()
	if err := handle.Select(best.Name, best.Name); err != nil {
		fmt.Println("!! 切换失败:", err)
		return
	}
	fmt.Printf("已自动选择最快节点: %s (%dms)\n", best.Name, best.Ping)
	if status.Running {
		fmt.Println("!! 加速仍在用旧节点，输入 r 重启加速后生效")
	}
}

// handle 处理一条命令，返回 (是否退出, 是否进入实时监控)。
func handleCommand(cmd string) (quit, watch bool) {
	switch {
	case cmd == "q":
		status, _ := snapshot()
		if status.Running && handle.IsOwner() && !state.confirmQuit {
			state.confirmQuit = true
			fmt.Println("加速仍在进行：再输入一次 q 确认退出（会停止加速）；输入 t 可先停止加速")
			return false, false
		}
		if status.Running && !handle.IsOwner() {
			fmt.Printf("本进程只是前端：退出后加速仍由核心（PID %d）继续，需要停止请先输入 t\n", handle.Info().PID)
		}
		if handle.IsOwner() {
			stopTunnel(true)
		}
		fmt.Println("bye")
		return true, false
	case cmd == "s":
		startTunnel()
	case cmd == "t":
		stopTunnel(false)
	case cmd == "r":
		if err := handle.Restart(); err != nil {
			fmt.Println("!! 重启失败:", err)
			return false, false
		}
		fmt.Println("已用当前节点重启加速")
	case cmd == "p":
		fmt.Println("正在测速（最多 3 秒/节点）...")
		runCommand(handle.PingAll)
	case cmd == "a":
		chooseFastest()
	case cmd == "w":
		return false, true
	case cmd == "d":
		config.Debug.Store(!config.Debug.Load())
		fmt.Println("debug =", config.Debug.Load(), "(仅本进程生效；启动加速的核心决定日志级别)")
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
		importPeer(strings.TrimPrefix(cmd, "i"))
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

// watchCoreEvents 把核心事件打印出来：GUI 里的操作在本终端也能立刻看到。
func watchCoreEvents(ctx context.Context) {
	defer recordPanic("watchCoreEvents")
	events, err := handle.Events(ctx)
	if err != nil {
		return
	}
	for event := range events {
		fmt.Printf("\n[核心] %s\n", event.Message)
	}
}

func cleanup() {
	cleanupOnce.Do(func() {
		if handle == nil {
			return
		}
		if handle.IsOwner() {
			_ = handle.Stop()
		}
		handle.Close()
	})
}

func main() {
	configPath := flag.String("config", "", "指定配置文件路径（默认：可执行文件同级或 ~/.gpp/config.json）")
	flag.Parse()
	if *configPath != "" {
		config.SetPath(*configPath)
	}

	state.useANSI = enableANSI()

	attached, err := control.Attach("tui", version, filepath.Join(config.UserDir(), "gpp-tui.log"))
	if err != nil {
		fmt.Println("!! 无法接入 gpp 控制面:", err)
		fmt.Println("   如果确认没有其他 gpp 在运行，可删除", control.LockPath(), "后重试")
		return
	}
	handle = attached
	if handle.IsOwner() {
		if loadErr := handle.Engine().Load(); loadErr != nil {
			fmt.Println("!! 配置提示:", loadErr)
		}
	}

	fmt.Println("gpp-tui 启动（配置与状态由核心统一维护，可与 GUI 同时打开）")
	if handle.IsOwner() && !isAdmin() {
		fmt.Println("!! 本进程是核心，但当前不是管理员身份：启动加速时会创建虚拟网卡失败，请以管理员身份重新运行")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n收到退出信号，正在释放资源...")
		cleanup()
		os.Exit(0)
	}()

	go watchCoreEvents(context.Background())

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
				state.watch = false
				continue
			}
			quit, watch := func() (quit, watch bool) {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("!! 命令执行异常:", r)
					}
				}()
				return handleCommand(cmd)
			}()
			if quit {
				cleanup()
				return
			}
			if !watch {
				state.confirmQuit = false
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
