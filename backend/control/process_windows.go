//go:build windows

package control

import (
	"golang.org/x/sys/windows"
)

// stillActive 是 Windows 对"进程仍在运行"返回的退出码。
const stillActive = 259

// processAlive 判断 PID 对应的进程是否仍然在运行。
//
// 注意不能只看 OpenProcess 是否成功：进程被强杀后，只要还有句柄没释放，
// 内核对象会短暂保留，OpenProcess(Microsoft 文档同样如此) 依然能成功，
// 于是"已崩溃的核心"会被误判为存活，新实例永远接管不了。
// 这里进一步用 GetExitCodeProcess 判断是否仍为 STILL_ACTIVE。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// PID 不存在（ERROR_INVALID_PARAMETER）或无权访问：都按"无法确认存活"处理。
		// 健康检查才是主要判据，这里只在"控制面无响应"时才用来判断锁是否陈旧。
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	if err = windows.GetExitCodeProcess(handle, &code); err != nil {
		return true // 查不到退出码，保守认为还活着
	}
	return code == stillActive
}
