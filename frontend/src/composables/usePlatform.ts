// 自绘标题栏只在 "无边框窗口" 下才需要渲染，而无边框只在 Windows 上开启
// （见仓库根目录的 window_windows.go / window_other.go）。
//
// Wails 各平台的 WebView 都会在 userAgent 里带上真实平台名：
// Windows 是 "Windows NT 10.0"，macOS 是 "Macintosh"，Linux 是 "X11; Linux"。
// 这里用同步判断而不是让后端推事件，是为了避免 "窗口已经显示、事件还没到" 时
// 闪一下系统标题栏或者两条标题栏同时出现。
export const usesCustomChrome = /Windows/i.test(navigator.userAgent)
