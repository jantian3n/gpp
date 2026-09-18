package main

import (
	"embed"
	"github.com/danbai225/gpp/backend/config"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"net"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var logo []byte

// singleInstanceAddr 是 GUI 的单实例端口。先启动的实例在这里监听；后启动的实例
// 只会让它把窗口显示出来，然后自己直接退出（见 main 开头的探测）。
// app.go 的托盘初始化也在这个端口上做监听。
const singleInstanceAddr = "127.0.0.1:54713"

func main() {
	dial, err := net.Dial("tcp", singleInstanceAddr)
	if err == nil {
		_, _ = dial.Write([]byte("SHOW_WINDOW"))
		_ = dial.Close()
		return
	}
	config.InitConfig()
	// Create an instance of the app structure
	app := NewApp()
	// 注：隧道的启停由 app.shutdown / 托盘菜单处理（作为前端退出时不应该停掉别人持有的隧道），
	// 因此这里不再 defer app.Stop()。

	// Create application with options
	err = wails.Run(&options.App{
		Title:         "gpp",
		Width:         360,
		Height:        520,
		DisableResize: true,
		// 窗口固定尺寸，所以标题栏上只有最小化和关闭，没有最大化 —— 和 Win11 原生行为一致。
		Frameless: nativeChrome,
		// Windows 上配合 Mica 材质使用；其他平台为 nil（见 window_*.go）。
		Windows:           platformWindowOptions(),
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// A=0 让窗口自身不画底色，Windows 上的系统材质才能透上来；
		// RGB 取 Win11 的浅色基准底色 #F3F3F3，在没有材质的平台上闪屏更小。
		BackgroundColour: &options.RGBA{R: 243, G: 243, B: 243, A: 0},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
