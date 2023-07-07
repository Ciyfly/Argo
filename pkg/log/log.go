// copy https://github.com/qo0581122/go-logrus-document
package log

import (
	"os"

	"github.com/sirupsen/logrus"
)

var Logger *logrus.Logger

func Init(debug bool, quiet bool) {
	Logger = logrus.New()
	Logger.SetOutput(os.Stderr)
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
