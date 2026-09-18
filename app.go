package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/danbai225/gpp/backend/control"
	"github.com/danbai225/gpp/backend/data"
	"github.com/danbai225/gpp/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是 GUI 前端：自己不持有隧道，一切读写都通过本地控制面（backend/control）。
// 这样 GUI 与 TUI 看到的是同一份状态，也不会出现"两个界面各起一条隧道"。
type App struct {
	ctx       context.Context
	handle    *control.Handle
	attachErr error

	mu       sync.Mutex
	warnings []string
	done     chan struct{}
	doneOnce sync.Once
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{done: make(chan struct{})}
}

// buildVersion 返回版本号（优先用构建信息，便于排查"用户跑的到底是哪版"）。
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// recordPanic 记录后台 goroutine 的 panic，避免程序无声闪退后没有任何线索。
func recordPanic(where string) {
	if r := recover(); r != nil {
		_ = os.WriteFile(filepath.Join(config.UserDir(), "panic.log"),
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
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, warning := range warnings {
		if strings.TrimSpace(warning) != "" {
			a.warnings = append(a.warnings, warning)
		}
	}
}

func (a *App) takeWarnings() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.warnings) == 0 {
		return ""
	}
	warning := strings.Join(a.warnings, "\n")
	a.warnings = nil
	return warning
}

func (a *App) systemTray() {
	systray.SetIcon(logo) // read the icon from a file
	show := systray.AddMenuItem("显示窗口", "显示窗口")
	systray.AddSeparator()
	exit := systray.AddMenuItem("退出加速器", "退出加速器")
	show.Click(func() { runtime.WindowShow(a.ctx) })
	exit.Click(func() {
		// 本窗口只是前端时，隧道由另一个核心（例如终端版）持有：
		// 直接退出会让"加速还在跑"变得不可见，这里明确问一次。
		if a.isFrontend() && a.isRunning() {
			choice, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
				Type:    runtime.QuestionDialog,
				Title:   "退出 gpp",
				Message: fmt.Sprintf("当前加速由另一个 gpp 核心（PID %d，来自 %s）持有。\n\n仅退出窗口：加速继续进行\n停止加速并退出：立即断开", a.handle.Info().PID, a.handle.Info().Kind),
				Buttons: []string{"仅退出窗口", "停止加速并退出"},
			})
			if err == nil && strings.Contains(choice, "停止") {
				a.Stop()
			}
		} else {
			a.Stop()
		}
		runtime.Quit(a.ctx)
		systray.Quit()
		time.Sleep(time.Second)
		os.Exit(0)
	})
	systray.SetOnClick(func(menu systray.IMenu) { runtime.WindowShow(a.ctx) })
	go func() {
		defer recordPanic("systemTray")
		listener, err := net.Listen("tcp", singleInstanceAddr)
		if err != nil {
			// 端口被占用通常意味着已有一个 gpp GUI 在运行（main.go 就是靠它实现单实例）。
			// 这里必须 return：旧版本漏了 return，会在 nil listener 上调用 Accept 直接 panic。
			return
		}
		defer func() { _ = listener.Close() }()
		for {
			conn, err := listener.Accept()
			if err != nil {
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

// watchEvents 把核心事件转成 wails 事件推给网页，界面无需等下一次轮询。
func (a *App) watchEvents() {
	defer recordPanic("watchEvents")
	events, err := a.handle.Events(a.ctx)
	if err != nil {
		a.addWarning("事件订阅失败：" + err.Error())
		return
	}
	for event := range events {
		runtime.EventsEmit(a.ctx, "gpp:event", event)
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go systray.Run(a.systemTray, func() {})

	handle, err := control.Attach("gui", buildVersion(), filepath.Join(config.UserDir(), "gpp-gui.log"))
	if err != nil {
		a.attachErr = err
		a.addWarning(err.Error())
		a.dialog("启动失败", err.Error())
		return
	}
	a.handle = handle
	if handle.IsOwner() {
		// 本进程是核心：加载配置（配置读取的告警会通过控制面带给所有前端）
		if loadErr := handle.Engine().Load(); loadErr != nil {
			a.addWarning(loadErr.Error())
		}
	} else {
		a.addWarning(fmt.Sprintf("已连接到正在运行的 gpp 核心（PID %d，来自 %s），加速状态由它统一维护",
			handle.Info().PID, handle.Info().Kind))
	}
	go a.watchEvents()
}

func (a *App) shutdown(_ context.Context) {
	a.doneOnce.Do(func() { close(a.done) })
	// 只有核心才负责停隧道：作为前端退出时，隧道继续由核心持有
	if a.handle != nil && a.handle.IsOwner() {
		_ = a.handle.Stop()
	}
	if a.handle != nil {
		a.handle.Close()
	}
}

func (a *App) isFrontend() bool {
	return a.handle != nil && !a.handle.IsOwner()
}

func (a *App) isRunning() bool {
	if a.handle == nil {
		return false
	}
	status, err := a.handle.Status()
	return err == nil && status.Running
}

func (a *App) status() (*data.Status, error) {
	if a.handle == nil {
		if a.attachErr != nil {
			return nil, a.attachErr
		}
		return nil, fmt.Errorf("控制面尚未就绪")
	}
	return a.handle.Status()
}

func (a *App) Status() *data.Status {
	status, err := a.status()
	if err != nil {
		// 核心不见了也要给前端一个可展示的状态，而不是空白界面
		return &data.Status{Warning: "与 gpp 核心失去连接：" + err.Error()}
	}
	if warning := a.takeWarnings(); warning != "" {
		if status.Warning == "" {
			status.Warning = warning
		} else {
			status.Warning = warning + "\n" + status.Warning
		}
	}
	return status
}

func (a *App) List() []*config.Peer {
	if a.handle == nil {
		return nil
	}
	// GUI 的节点下拉框按延迟展示，方便挑最快线路
	peers, err := a.handle.PeersByPing()
	if err != nil {
		return nil
	}
	return peers
}

func (a *App) PingAll() {
	if a.handle != nil {
		_ = a.handle.PingAll()
	}
}

func (a *App) Add(token string) string {
	if a.handle == nil {
		return "控制面尚未就绪"
	}
	if err := a.handle.Import(token); err != nil {
		a.dialog("导入错误", err.Error())
		return err.Error()
	}
	return "ok"
}

func (a *App) Del(name string) string {
	if a.handle == nil {
		return "控制面尚未就绪"
	}
	if err := a.handle.Delete(name); err != nil {
		return err.Error()
	}
	return "ok"
}

func (a *App) SetPeer(game, httpPeer string) string {
	if a.handle == nil {
		return "控制面尚未就绪"
	}
	if err := a.handle.Select(game, httpPeer); err != nil {
		a.dialog("保存错误", err.Error())
		return err.Error()
	}
	return "ok"
}

// Start 启动加速（通过控制面，由核心真正建立隧道）。
func (a *App) Start() string {
	if a.handle == nil {
		return "控制面尚未就绪"
	}
	alreadyRunning := false
	if status, err := a.handle.Status(); err == nil {
		alreadyRunning = status.Running
	}
	if err := a.handle.Start(); err != nil {
		a.dialog("加速失败", err.Error())
		return err.Error()
	}
	if alreadyRunning {
		return "running"
	}
	return "ok"
}

// Stop 停止加速。
func (a *App) Stop() string {
	if a.handle == nil {
		return "not running"
	}
	if err := a.handle.Stop(); err != nil {
		a.dialog("停止失败", err.Error())
		return err.Error()
	}
	return "ok"
}
