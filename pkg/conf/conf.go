package conf

import (
	"argo/pkg/log"
	"argo/pkg/utils"
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
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
  tabtimeout: 15 # tab页面最长时间
  browsertimeout: 600 # 浏览器运行最长时间
  maxdepth: 3 # 爬行最大深度
  user_agent: ""
auto:
  slow: 1000 # 事件触发的延迟时间
  filter: ["lougout", "登出", "reset"] # 包含这种字符的就不进行触发事件

`

type Conf struct {
	LoginConf        LoginConf   `yaml:"login"`
	BrowserConf      BrowserConf `yaml:"browser"`
	AutoConf         AutoConf    `yaml:"auto"`
	InjectScriptPath string
	ResultConf       ResultConf
	PlaybackPath     string
	TestPlayBack     bool
	TargetList       []string
	Dev              bool
	NoReqRspStr      bool
	Quiet            bool
}

// 保存的格式
type ResultConf struct {
	OutputDir    string
	Format       string
	Name         string
	MergedOutput string
}

// 默认的用户名密码
type LoginConf struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Email    string `yaml:"email"`
	Phone    string `yaml:"phone"`
}

// 浏览器参数
type BrowserConf struct {
	UnHeadless     bool   `yaml:"unheadless"`
	Trace          bool   `yaml:"trace"`
	TabCount       int    `yaml:"tab_count"`
	Proxy          string `yaml:"proxy"`
	TabTimeout     int    `yaml:"tab_timeout"`
	BrowserTimeout int    `yaml:"browser_timeout"`
	MaxDepth       int    `yaml:"max_depth"`
	Chrome         string `yaml:"chrome"`
	Remote         string `yaml:"remote"`
	UserAgent      string `yaml:"user_agent"`
}

// auto 自动触发的一些参数
type AutoConf struct {
	Slow   float64  `yaml:"slow"`
	Filter []string `yaml:"filter"`
}

func readYamlConfig(configFile string) {
	// 加载config

	yamlFile, err := ioutil.ReadFile(configFile)
	if err != nil {
		fmt.Printf("load config, fail to read 'config.yaml': %v\n", err)
	}
	GlobalConfig = &Conf{}
	err = yaml.Unmarshal(yamlFile, GlobalConfig)
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
	fmt.Println("argo create default config.yml")
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
	GlobalConfig.TargetList = make([]string, 0)
}

func MergeArgs(c *cli.Context) {
	target := c.String("target")
	targetsFile := c.String("targetsfile")
	unheadless := c.Bool("unheadless")
	trace := c.Bool("entrace")
	slow := c.Float64("slow")
	username := c.String("username")
	password := c.String("password")
	proxy := c.String("proxy")
	tabCount := c.Int("tabcount")
	tabTimeout := c.Int("tabtimeout")
	browserTimeout := c.Int("browsertimeout")
	chrome := c.String("chrome")
	remote := c.String("remote")
	userAgent := c.String("userAgent")
	// 回放
	playback := c.String("playback")
	testPlayback := c.Bool("testplayback")
	// 处理结果参数
	save := c.String("save")
	format := c.String("format")
	outputDir := c.String("outputdir")
	mergedOutput := c.String("mergedOutput")
	//静默输出
	quiet := c.Bool("quiet")
	// debug dev
	devMode := c.Bool("dev")

	// 优化控制
	norrs := c.Bool("norrs")
	maxDepth := c.Int("maxdepth")

	// 目标
	if target != "" {
		GlobalConfig.TargetList = append(GlobalConfig.TargetList, target)
	}
	if targetsFile != "" {
		if utils.IsExist(targetsFile) {
			tf, err := os.Open(targetsFile)
			if err != nil {
				log.Logger.Errorf("targetsfile open error: %s", targetsFile)
				os.Exit(1)
			}
			defer tf.Close()
			br := bufio.NewReader(tf)
			for {
				line, _, c := br.ReadLine()
				if c == io.EOF {
					break
				}
				lineStr := strings.Replace(string(line), "\n", "", -1)
				if lineStr == "" {
					continue
				}
				GlobalConfig.TargetList = append(GlobalConfig.TargetList, lineStr)
			}
		} else {
			log.Logger.Errorf("targetsfile not exist: %s", targetsFile)
		}
	}
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
	if chrome != GlobalConfig.BrowserConf.Chrome {
		GlobalConfig.BrowserConf.Chrome = chrome
	}
	if remote != GlobalConfig.BrowserConf.Remote {
		GlobalConfig.BrowserConf.Remote = remote
	}
	if userAgent != GlobalConfig.BrowserConf.UserAgent {
		GlobalConfig.BrowserConf.UserAgent = userAgent
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
	GlobalConfig.ResultConf.OutputDir = outputDir
	GlobalConfig.ResultConf.MergedOutput = mergedOutput
	//dev
	GlobalConfig.Dev = devMode

	// 静默
	GlobalConfig.Quiet = quiet

	// 优化控制
	GlobalConfig.NoReqRspStr = norrs
	GlobalConfig.BrowserConf.MaxDepth = maxDepth

}
