package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloverstd/tcping/ping"
	"github.com/danbai225/gpp/backend/client"
	"github.com/danbai225/gpp/backend/config"
	"github.com/danbai225/gpp/backend/data"
	"github.com/danbai225/gpp/systray"
	box "github.com/sagernet/sing-box"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx      context.Context
	conf     *config.Config
	gamePeer *config.Peer
	httpPeer *config.Peer
	box      *box.Box
	lock     sync.Mutex // 保护 box/gamePeer/httpPeer 的启停
	confLock sync.Mutex // 保护 conf.PeerList 的增删改查
	pingLock sync.Mutex // 保护 Peer.Ping 读写
	done     chan struct{}
	doneOnce sync.Once
	warnLock sync.Mutex
	warnings []string
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		conf: config.Default(),
		done: make(chan struct{}),
	}
}

// recordPanic 记录后台 goroutine 的 panic，避免程序无声闪退后没有任何线索。
// 日志写在用户目录（而不是当前工作目录），保证任何启动方式都能找到。
func recordPanic(where string) {
	if r := recover(); r != nil {
		path := filepath.Join(config.UserDir(), "panic.log")
		_ = os.WriteFile(path,
			[]byte(fmt.Sprintf("[%s] %s panic: %v\n%s\n", time.Now().Format(time.RFC3339), where, r, debug.Stack())),
			0o644)
	}
}

func (a *App) dialog(title, message string) {
	if a.ctx == nil {
		return
	}
	_, _ = runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:    runtime.ErrorDialog,
		Title:   title,
		Message: message,
	})
}

func (a *App) addWarning(warnings ...string) {
	a.warnLock.Lock()
	defer a.warnLock.Unlock()
	for _, w := range warnings {
		if strings.TrimSpace(w) != "" {
			a.warnings = append(a.warnings, w)
		}
	}
}

// takeWarnings 取走累计告警（前端只提示一次）。
func (a *App) takeWarnings() string {
	a.warnLock.Lock()
	defer a.warnLock.Unlock()
	if len(a.warnings) == 0 {
		return ""
	}
	warning := strings.Join(a.warnings, "\n")
	a.warnings = nil
	return warning
}

// resolvePeers 保证 conf 里的选中节点真实存在，并同步到 gamePeer/httpPeer。
// 旧版本在"节点被删除/订阅更新"后会把 gamePeer/httpPeer 留成 nil，点开始加速就 panic。
func (a *App) resolvePeers() {
	a.confLock.Lock()
	defer a.confLock.Unlock()
	a.addWarning(a.conf.Normalize()...)
	a.gamePeer = a.conf.FindPeer(a.conf.GamePeer)
	a.httpPeer = a.conf.FindPeer(a.conf.HTTPPeer)
	if a.httpPeer == nil {
		a.httpPeer = a.gamePeer
	}
}

func (a *App) systemTray() {
	systray.SetIcon(logo) // read the icon from a file
	show := systray.AddMenuItem("显示窗口", "显示窗口")
	systray.AddSeparator()
	exit := systray.AddMenuItem("退出加速器", "退出加速器")
	show.Click(func() { runtime.WindowShow(a.ctx) })
	exit.Click(func() {
		a.Stop()
		runtime.Quit(a.ctx)
		systray.Quit()
		time.Sleep(time.Second)
		os.Exit(0)
	})
	systray.SetOnClick(func(menu systray.IMenu) { runtime.WindowShow(a.ctx) })
	go func() {
		defer recordPanic("systemTray")
		listener, err := net.Listen("tcp", "127.0.0.1:54713")
		if err != nil {
			// 端口被占用通常意味着已有一个 gpp 在运行（main.go 就是靠它实现单实例）。
			// 这里必须 return：旧版本漏了 return，会在 nil listener 上调用 Accept 直接 panic。
			return
		}
		defer func() { _ = listener.Close() }()
		for {
			conn, err := listener.Accept()
			if err != nil {
				// 监听句柄失效时退出循环，避免刷屏式弹窗
				return
			}
			buffer := make([]byte, 1024)
			n, err := conn.Read(buffer)
			if err == nil && string(buffer[:n]) == "SHOW_WINDOW" {
				runtime.WindowShow(a.ctx)
			}
			_ = conn.Close()
		}
	}()
}

func (a *App) testPing() {
	defer recordPanic("testPing")
	a.PingAll()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.PingAll()
		}
	}
}

func (a *App) shutdown(_ context.Context) {
	a.doneOnce.Do(func() { close(a.done) })
	a.Stop()
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go systray.Run(a.systemTray, func() {})
	// LoadConfig 永远返回可用配置：这里的 err 只是告警，绝不能因此丢弃配置。
	loadConfig, err := config.LoadConfig()
	if loadConfig != nil {
		a.conf = loadConfig
	}
	a.addWarning(strings.Split(errString(err), "\n")...)
	a.resolvePeers()
	if err != nil {
		a.dialog("配置提示", err.Error())
	}
	go a.testPing()
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func copyPeer(p *config.Peer) *config.Peer {
	if p == nil {
		return nil
	}
	clone := *p
	return &clone
}

func (a *App) PingAll() {
	a.lock.Lock()
	running := a.box != nil
	a.lock.Unlock()
	if running {
		return
	}
	a.confLock.Lock()
	peers := a.conf.Peers()
	a.confLock.Unlock()

	group := sync.WaitGroup{}
	for _, peer := range peers {
		if peer == nil || peer.Protocol == "direct" {
			continue
		}
		group.Add(1)
		go func(p *config.Peer) {
			defer group.Done()
			ms := pingPort(p.Addr, p.Port)
			a.pingLock.Lock()
			p.Ping = ms
			a.pingLock.Unlock()
		}(peer)
	}
	group.Wait()
}

func (a *App) Status() *data.Status {
	a.lock.Lock()
	running := a.box != nil
	a.lock.Unlock()

	// confLock 保护节点内容（Normalize 会改名字），pingLock 保护 Ping 读写
	a.confLock.Lock()
	a.pingLock.Lock()
	game := copyPeer(a.gamePeer)
	httpPeer := copyPeer(a.httpPeer)
	a.pingLock.Unlock()
	a.confLock.Unlock()

	up, down := data.Traffic()
	return &data.Status{
		Running:  running,
		GamePeer: game,
		HttpPeer: httpPeer,
		Up:       up,
		Down:     down,
		Warning:  a.takeWarnings(),
	}
}

// List 返回按延迟排序的节点副本：未测速/测不到的节点排在最后，
// 避免旧版本里"hysteria2 测不到 TCP 延迟 → ping=0 → 显示成最快节点"。
func (a *App) List() []*config.Peer {
	a.confLock.Lock()
	a.pingLock.Lock()
	peers := a.conf.Peers()
	list := make([]*config.Peer, 0, len(peers))
	for _, peer := range peers {
		if peer == nil {
			continue
		}
		list = append(list, copyPeer(peer))
	}
	a.pingLock.Unlock()
	a.confLock.Unlock()

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

func (a *App) Add(token string) string {
	a.confLock.Lock()
	err := config.AddPeer(a.conf, token)
	if err != nil {
		a.confLock.Unlock()
		a.dialog("导入错误", err.Error())
		return err.Error()
	}
	a.confLock.Unlock()

	a.resolvePeers()
	return "ok"
}

func (a *App) Del(Name string) string {
	a.confLock.Lock()
	err := config.DelPeer(a.conf, Name)
	a.confLock.Unlock()
	if err != nil {
		return err.Error()
	}
	// 删除后重新校正选中节点，保证不会留下悬空的名字导致后续 panic
	a.resolvePeers()
	return "ok"
}

func (a *App) SetPeer(game, httpPeer string) string {
	a.lock.Lock()
	defer a.lock.Unlock()

	a.confLock.Lock()
	changed := false
	if peer := a.conf.FindPeer(game); peer != nil {
		a.gamePeer = peer
		a.conf.GamePeer = peer.Name
		changed = true
	}
	if peer := a.conf.FindPeer(httpPeer); peer != nil {
		a.httpPeer = peer
		a.conf.HTTPPeer = peer.Name
		changed = true
	}
	if !changed {
		a.confLock.Unlock()
		return "节点不存在，请刷新节点列表后重试"
	}
	err := config.SaveConfig(a.conf)
	a.confLock.Unlock()
	if err != nil {
		a.dialog("保存错误", err.Error())
		return err.Error()
	}
	return "ok"
}

// Start 启动加速
func (a *App) Start() (result string) {
	a.lock.Lock()
	defer a.lock.Unlock()
	defer func() {
		if r := recover(); r != nil {
			recordPanic("App.Start")
			a.box = nil
			result = fmt.Sprintf("加速失败（内部异常，详见 panic.log）：%v", r)
		}
	}()
	if a.box != nil {
		return "running"
	}
	a.resolvePeers()
	if a.gamePeer == nil {
		return "还没有可用节点，请先导入节点"
	}
	b, err := client.Client(a.gamePeer, a.httpPeer, a.conf.ProxyDNS, a.conf.LocalDNS, a.conf.Rules)
	if err != nil {
		message := client.ExplainError(err).Error()
		a.dialog("加速失败", message)
		return message
	}
	if err = b.Start(); err != nil {
		_ = b.Close()
		message := client.ExplainError(err).Error()
		a.dialog("加速失败", message)
		return message
	}
	a.box = b
	return "ok"
}

// Stop 停止加速
func (a *App) Stop() string {
	a.lock.Lock()
	defer a.lock.Unlock()
	if a.box == nil {
		return "not running"
	}
	err := a.box.Close()
	if err != nil {
		a.dialog("停止失败", err.Error())
		return err.Error()
	}
	a.box = nil
	return "ok"
}

func pingPort(host string, port uint16) uint {
	tcPing := ping.NewTCPing()
	tcPing.SetTarget(&ping.Target{
		Host:     host,
		Port:     int(port),
		Counter:  1,
		Interval: time.Millisecond * 200,
		Timeout:  time.Second * 3,
	})
	start := tcPing.Start()
	<-start
	result := tcPing.Result()
	return uint(result.Avg().Milliseconds())
}
