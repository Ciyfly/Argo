// copy https://github.com/qo0581122/go-logrus-document
package log

import (
	"os"
	"runtime"

	"github.com/mattn/go-colorable"
	"github.com/sirupsen/logrus"
)

var Logger *logrus.Logger

func Init(debug bool, quiet bool) {
	Logger = logrus.New()
	if runtime.GOOS == "windows" {
		// Windows 控制台默认不支持 ANSI，需要先开启虚拟终端再做转义
		enableVirtualTerminal()
		Logger.SetOutput(colorable.NewColorable(os.Stderr))
	} else {
		Logger.SetOutput(os.Stderr)
	}
	// Logger.SetReportCaller(true)         //开启返回函数名和行号
	Logger.SetFormatter(&LogFormatter{})
	Logger.SetLevel(logrus.InfoLevel)
	if debug {
		Logger.SetLevel(logrus.DebugLevel)
	}
	if quiet {
		Logger.SetLevel(logrus.PanicLevel)
	}
}
