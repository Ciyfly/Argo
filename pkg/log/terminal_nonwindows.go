//go:build !windows

package log

// enableVirtualTerminal 在非 Windows 平台无须处理。
func enableVirtualTerminal() {}
