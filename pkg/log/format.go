// copy
package log

import (
	"bytes"
	"fmt"
	"path"

	logrus "github.com/sirupsen/logrus"
)

//颜色
const (
	red    = 91
	yellow = 93
	blue   = 36
	gray   = 90
)

type LogFormatter struct{}

//实现Formatter(entry *logrus.Entry) ([]byte, error)接口
func (t *LogFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	//根据不同的level去展示颜色
	var levelColor int
	switch entry.Level {
	case logrus.DebugLevel, logrus.TraceLevel:
		levelColor = gray
	case logrus.WarnLevel:
		levelColor = yellow
	case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
		levelColor = red
	default:
		levelColor = blue
	}
	var b *bytes.Buffer
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}
	//自定义日期格式
	timestamp := entry.Time.Format("2006-01-02 15:04:05")
	if entry.HasCaller() {
		//自定义文件路径
		funcVal := entry.Caller.Function
		fileVal := fmt.Sprintf("%s:%d", path.Base(entry.Caller.File), entry.Caller.Line)
		//自定义输出格式
		fmt.Fprintf(b, "[%s] \x1b[%dm[%s] %s %s %s\x1b[0m\n", timestamp, levelColor, entry.Level, fileVal, funcVal, entry.Message)
	} else {
		fmt.Fprintf(b, "\x1b[%dm[%s] [%s]  %s\x1b[0m\n", levelColor, timestamp, entry.Level, entry.Message)
	}
	return b.Bytes(), nil
}
