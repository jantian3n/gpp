// gpp-tui: gpp 加速器的终端版客户端，带完整日志输出，便于排查问题。
//
// 用法: 以管理员身份运行 gpp-tui.exe（TUN 需要管理员权限）
// 日志: 程序目录下 gpp-tui.log（sing-box 运行日志），debug 开启后另有 debug.log
package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/danbai225/gpp/backend/client"
	"github.com/danbai225/gpp/backend/config"
	box "github.com/sagernet/sing-box"
	netutils "github.com/shirou/gopsutil/v3/net"
)

var (
	conf     *config.Config
	gamePeer *config.Peer
	httpPeer *config.Peer
	instance *box.Box
)

func peerByName(name string) *config.Peer {
	for _, p := range conf.PeerList {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func traffic() (up, down uint64) {
	counters, _ := netutils.IOCounters(true)
	for _, c := range counters {
		if c.Name == "utun225" || c.Name == "gpp" {
			up += c.BytesSent
			down += c.BytesRecv
		}
	}
	return
}

func draw() {
	fmt.Println("================ gpp 加速器 (TUI) ================")
	if p, _ := filepath.Abs(client.LogOutput); p != "" {
		fmt.Printf("日志文件: %s | debug: %v\n", p, config.Debug.Load())
	}
	fmt.Println("---------------------------------------------------")
	for i, p := range conf.PeerList {
		tags := ""
		if gamePeer != nil && p.Name == gamePeer.Name {
			tags += "[Game]"
		}
		if httpPeer != nil && p.Name == httpPeer.Name {
			tags += "[Http]"
		}
		addr := fmt.Sprintf("%s:%d", p.Addr, p.Port)
		if p.Protocol == "direct" {
			addr = "-"
		}
		ping := strconv.FormatUint(uint64(p.Ping), 10) + "ms"
		if p.Protocol == "direct" {
			ping = "-"
		}
		fmt.Printf(" %2d  %-12s %-11s %-21s %-7s %s\n", i+1, p.Name, p.Protocol, addr, ping, tags)
	}
	fmt.Println("---------------------------------------------------")
	if instance != nil {
		up, down := traffic()
		fmt.Printf("状态: 加速中 | Game: %s | Http: %s | ↑%s ↓%s\n",
			nameOf(gamePeer), nameOf(httpPeer), humanBytes(up), humanBytes(down))
	} else {
		fmt.Printf("状态: 未加速 | Game: %s | Http: %s\n", nameOf(gamePeer), nameOf(httpPeer))
	}
	fmt.Println("命令: 数字=选Game | h数字=选Http | s=开始 | t=停止 | p=测延迟 | d=debug | ?=帮助 | q=退出")
}

func nameOf(p *config.Peer) string {
	if p == nil {
		return "未选择"
	}
	return p.Name
}

func humanBytes(n uint64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func selectPeer(n int, isHTTP bool) {
	if n < 1 || n > len(conf.PeerList) {
		fmt.Println("!! 序号超出范围")
		return
	}
	p := conf.PeerList[n-1]
	if isHTTP {
		httpPeer = p
		conf.HTTPPeer = p.Name
		fmt.Println("Http 节点 ->", p.Name)
	} else {
		gamePeer = p
		conf.GamePeer = p.Name
		fmt.Println("Game 节点 ->", p.Name)
	}
	if err := config.SaveConfig(conf); err != nil {
		fmt.Println("!! 保存配置失败:", err)
	}
}

func startBox() {
	if instance != nil {
		fmt.Println("已在加速中")
		return
	}
	if gamePeer == nil {
		fmt.Println("!! 请先选择 Game 节点")
		return
	}
	hp := httpPeer
	if hp == nil {
		hp = gamePeer
	}
	b, err := client.Client(gamePeer, hp, conf.ProxyDNS, conf.LocalDNS, conf.Rules)
	if err != nil {
		fmt.Println("!! 构建失败:", err)
		return
	}
	if err = b.Start(); err != nil {
		fmt.Println("!! 加速失败:", err)
		_ = b.Close()
		return
	}
	instance = b
	fmt.Println("加速已启动")
}

func stopBox() {
	if instance == nil {
		fmt.Println("未在加速")
		return
	}
	if err := instance.Close(); err != nil {
		fmt.Println("!! 停止失败:", err)
		return
	}
	instance = nil
	fmt.Println("已停止")
}

func pingAll() {
	var wg sync.WaitGroup
	for _, p := range conf.PeerList {
		if p.Protocol == "direct" {
			continue
		}
		wg.Add(1)
		go func(p *config.Peer) {
			defer wg.Done()
			start := time.Now()
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", p.Addr, p.Port), 3*time.Second)
			if err == nil {
				_ = conn.Close()
				p.Ping = uint(time.Since(start).Milliseconds())
				fmt.Printf("  %-12s %dms\n", p.Name, p.Ping)
				return
			}
			// hysteria2 只监听 UDP，TCP 被拒说明主机在线，属正常现象
			var errno syscall.Errno
			refused := errors.Is(err, syscall.ECONNREFUSED) ||
				(errors.As(err, &errno) && errno == syscall.ECONNREFUSED) ||
				strings.Contains(strings.ToLower(err.Error()), "refused")
			if p.Protocol == "hysteria2" && refused {
				p.Ping = uint(time.Since(start).Milliseconds())
				fmt.Printf("  %-12s %dms (在线，hysteria2 走 UDP)\n", p.Name, p.Ping)
				return
			}
			p.Ping = 0
			fmt.Printf("  %-12s 不可达 (%v)\n", p.Name, err)
		}(p)
	}
	wg.Wait()
}

func handle(cmd string) {
	switch {
	case cmd == "q":
		stopBox()
		fmt.Println("bye")
	case cmd == "s":
		startBox()
	case cmd == "t":
		stopBox()
	case cmd == "p":
		pingAll()
	case cmd == "d":
		config.Debug.Store(!config.Debug.Load())
		fmt.Println("debug =", config.Debug.Load(), "(下次启动加速时生效，trace 日志写入 debug.log)")
	case cmd == "?":
		fmt.Println("数字=设Game节点  h数字=设Http节点  s=开始加速  t=停止  p=测延迟  d=debug日志  q=退出")
	case strings.HasPrefix(cmd, "h"):
		n, err := strconv.Atoi(strings.TrimPrefix(cmd, "h"))
		if err != nil {
			fmt.Println("!! 无法识别命令:", cmd)
			return
		}
		selectPeer(n, true)
	default:
		n, err := strconv.Atoi(cmd)
		if err != nil {
			fmt.Println("!! 无法识别命令:", cmd)
			return
		}
		selectPeer(n, false)
	}
}

func main() {
	logPath, _ := filepath.Abs("gpp-tui.log")
	client.LogOutput = logPath

	config.InitConfig()
	c, err := config.LoadConfig()
	if err != nil {
		fmt.Println("!! 配置加载错误:", err)
	}
	conf = c

	gamePeer = peerByName(conf.GamePeer)
	httpPeer = peerByName(conf.HTTPPeer)
	if gamePeer == nil && len(conf.PeerList) > 0 {
		gamePeer = conf.PeerList[0]
		conf.GamePeer = gamePeer.Name
	}
	if httpPeer == nil && gamePeer != nil {
		httpPeer = gamePeer
		conf.HTTPPeer = gamePeer.Name
	}
	_ = config.SaveConfig(conf)

	fmt.Println("gpp-tui 启动，sing-box 日志 ->", logPath)
	fmt.Println("如创建虚拟网卡失败，请确认本窗口是以管理员身份运行的。")

	reader := bufio.NewReader(os.Stdin)
	for {
		draw()
		fmt.Print("> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			stopBox()
			return
		}
		cmd := strings.TrimSpace(line)
		if cmd == "" {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("!! 命令执行异常:", r)
				}
			}()
			handle(cmd)
		}()
		if cmd == "q" {
			return
		}
	}
}
