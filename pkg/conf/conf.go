package conf

import (
	"argo/pkg/utils"
	"fmt"
	"os"
	"path"

	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
)

var GlobalConfig *Conf

var defaultYamlConfigStr = `login:
  username: "argo"
  password: "argo123"
  email: "argo@recar.com"
  phone: "18888888888"
browser:
  unheadless: false # 开启则界面
  trace: false # 有界面时显示点击了哪些
  tabcount: 10 # 最多开启多个tab页面
  proxy: ""
  tabtimeout: 180 # tab页面最长时间
  browsertimeout: 36000 # 浏览器运行最长时间
auto:
  slow: 1000 # 事件触发的延迟时间
  filter: ["lougout", "登出", "reset"] # 包含这种字符的就不进行触发事件
`

type Conf struct {
	LoginConf        LoginConf   `mapstructure:"login"`
	BrowserConf      BrowserConf `mapstructure:"browser"`
	AutoConf         AutoConf    `mapstructure:"auto"`
	InjectScriptPath string
	ResultConf       ResultConf
	PlaybackPath     string
	TestPlayBack     bool
}

// 保存的格式
type ResultConf struct {
	Format string
	Name   string
}

// 默认的用户名密码
type LoginConf struct {
	Username string
	Password string
	Email    string
	Phone    string
}

// 浏览器参数
type BrowserConf struct {
	UnHeadless     bool
	Trace          bool
	TabCount       int
	Proxy          string
	TabTimeout     int
	BrowserTimeout int
}

// auto 自动触发的一些参数
type AutoConf struct {
	Slow   float64
	Fliter []string
}

func readYamlConfig(configFile string) {
	// 加载config
	viper.SetConfigType("yaml")
	viper.SetConfigFile(configFile)

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Printf("load config, fail to read 'config.yaml': %v\n", err)
	}
	err = viper.Unmarshal(&GlobalConfig)
	if err != nil {
		fmt.Printf("load config, fail to parse 'config.yaml', check format: %v\n", err)
	}
}

func InitConfig() {
	// 这种情况下直接生成到程序当前目录
	configFile := path.Join(utils.GetCurrentDirectory(), "config.yml")
	dstFile, err := os.Create(configFile)
	if err != nil {
		fmt.Printf("init config error: %s", err)
		panic(err)
	}
	defer dstFile.Close()
	dstFile.WriteString(defaultYamlConfigStr)
	fmt.Print("argo create default config.yml")
}

func LoadConfig() {
	configDir := path.Join(utils.GetCurrentDirectory(), "configs")
	initConfigPath := path.Join(utils.GetCurrentDirectory(), "config.yml")
	configFile := path.Join(configDir, "config.yml")
	// 如果文件存在直接读取 不存在则初始化创建一个
	if utils.IsExist(configFile) {
		readYamlConfig(configFile)
	} else if utils.IsExist(initConfigPath) {
		readYamlConfig(initConfigPath)
	} else {
		InitConfig()
		readYamlConfig(initConfigPath)
	}
}

func MergeArgs(c *cli.Context) {
	unheadless := c.Bool("unheadless")
	trace := c.Bool("entrace")
	slow := c.Float64("slow")
	username := c.String("username")
	password := c.String("password")
	proxy := c.String("proxy")
	tabCount := c.Int("tabcount")
	tabTimeout := c.Int("tabtimeout")
	browserTimeout := c.Int("browsertimeout")
	// 回放
	playback := c.String("playback")
	testPlayback := c.Bool("testplayback")
	// 处理结果参数
	save := c.String("save")
	format := c.String("format")
	// 浏览器参数
	if unheadless != GlobalConfig.BrowserConf.UnHeadless {
		GlobalConfig.BrowserConf.UnHeadless = unheadless
	}
	if trace != GlobalConfig.BrowserConf.Trace {
		GlobalConfig.BrowserConf.Trace = trace
	}

	if tabCount != GlobalConfig.BrowserConf.TabCount {
		GlobalConfig.BrowserConf.TabCount = tabCount
	}
	if proxy != GlobalConfig.BrowserConf.Proxy {
		GlobalConfig.BrowserConf.Proxy = proxy
	}
	if tabTimeout != GlobalConfig.BrowserConf.TabTimeout {
		GlobalConfig.BrowserConf.TabTimeout = tabTimeout
	}
	if browserTimeout != GlobalConfig.BrowserConf.BrowserTimeout {
		GlobalConfig.BrowserConf.BrowserTimeout = browserTimeout
	}
	// 登录参数
	if username != GlobalConfig.LoginConf.Username {
		GlobalConfig.LoginConf.Username = username
	}
	if password != GlobalConfig.LoginConf.Password {
		GlobalConfig.LoginConf.Password = password
	}
	// auto
	if slow != GlobalConfig.AutoConf.Slow {
		GlobalConfig.AutoConf.Slow = slow
	}
	// playback
	GlobalConfig.PlaybackPath = playback
	GlobalConfig.TestPlayBack = testPlayback
	// 结果处理参数
	GlobalConfig.ResultConf.Name = save
	GlobalConfig.ResultConf.Format = format
}
