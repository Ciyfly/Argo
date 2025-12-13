//go:build windows

package log

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal 开启 Windows 控制台的 ANSI 支持，避免颜色串乱码。
func enableVirtualTerminal() {
	// stdErr 为空时直接返回，防止空指针
	if os.Stderr == nil {
		return
	}

	handle := windows.Handle(os.Stderr.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	// 忽略 SetConsoleMode 的错误，只要尝试过即可
	_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
