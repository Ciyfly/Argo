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

// defaultYamlConfigStr 是首次运行自动生成的 config.yml 内容。
//
// 注意：这里写下的值会被命令行参数的默认值覆盖（详见 MergeArgs），
// 也就是说想改这些参数，最可靠的方式是在命令行上显式传参。
var defaultYamlConfigStr = `# Argo 配置文件
#
# ⚠️ 重要：命令行参数的默认值会覆盖本文件里的同名配置。
#
# 合并规则是「命令行 != 默认值 就覆盖配置文件」，所以：
#   - 本文件里写了 proxy，但命令行没传 --proxy，proxy 会被清空；
#   - 本文件里写了 tabtimeout: 30，但命令行没传 --tabtimeout，会被改回默认值 15。
#
# 想让某个参数生效，要么每次在命令行显式传参，要么就接受它被默认值覆盖。
# 加 --debug 运行，日志里会打印实际生效的浏览器配置，方便核对。
login:
  username: "argo"
  password: "argo123"
  email: "argo@recar.com"
  phone: "18888888888"
browser:
  unheadless: false # 开启则界面
  trace: false # 有界面时显示点击了哪些
  tab_count: 16 # 最多开启多个tab页面（实测 4/10/16 检出率不变，16 时速度最快）
  proxy: ""
  tab_timeout: 15 # tab页面最长时间
  browser_timeout: 600 # 浏览器运行最长时间
  max_depth: 3 # 爬行最大深度
  user_agent: ""
  wait_login: false # 人工登录模式：强制有头，首页后暂停等回车再继续（验证码场景）
  push_proxy: "" # 被动扫描器地址，如 http://127.0.0.1:7777（xray webscan --listen）
  engine: "headless" # headless 全浏览器 | hybrid 文档页 Go 抓取分流（更快）
auto:
  slow: 1000 # 事件触发的延迟时间
  filter: ["lougout", "登出", "reset"] # 包含这种字符的就不进行触发事件
  tab_idle: 10 # 页面静默多少秒后关闭（还在出新链接就不关），0 用默认 10s
  spider: true # 有头模式下展示蜘蛛爬行动画（点击元素时蜘蛛爬过去点中它）

scope:
  crawl_subdomains: false # 是否把目标 host 的子域也纳入爬取范围
  include: [] # 额外入域的正则列表（对完整 URL 匹配），如 ['^https?://[a-z]+\.partner\.com/']
  exclude: [] # 强制出域的正则列表，如 ['/logout', '/reset']
  save_outscope: false # 是否把域外发现的 URL 单独输出到 <结果名>.outscope.txt

extract:
  enable: true # 从 JS 响应体提取 API 接口并入队爬取
  secrets: true # 从文本响应体检测密钥泄漏并输出
  secret_rules: [] # 追加的密钥规则 [{name, regex, severity}]，如 [{name: "内网token", regex: "inner_[0-9a-z]{20}", severity: "high"}]

form:
  rules: [] # 字段级填充规则（正则对 name/id/placeholder 匹配），如 [{match: "cardno", value: "110101199001011234"}]
  skip: ["captcha|verify_?code|validate_?code|短信验证码|验证码|otp"] # 命中即跳过的字段（正则）

`

type Conf struct {
	LoginConf        LoginConf   `yaml:"login"`
	BrowserConf      BrowserConf `yaml:"browser"`
	AutoConf         AutoConf    `yaml:"auto"`
	ScopeConf        ScopeConf   `yaml:"scope"`
	ExtractConf      ExtractConf `yaml:"extract"`
	FormConf         FormConf    `yaml:"form"`
	InjectScriptPath string
	ResultConf       ResultConf
	PlaybackPath     string
	TestPlayBack     bool
	TargetList       []string
	Dev              bool
	NoReqRspStr      bool
	Quiet            bool
	// pprof 调试服务，默认关闭
	Pprof     bool
	PprofAddr string
	// Cookies 预置会话 Cookie，格式 name=value 或 name=value@domain。
	// 浏览器启动后注入，之后页面导航/XHR/表单提交全会带上——
	// 登录后才能访问的入口（会员区、管理后台）靠这个才能被发现。
	Cookies []string
	// FuzzConf 常见路径探测
	FuzzConf FuzzConf
	// WebConsole 启用内置 web 控制台（--web）
	WebConsole bool
	// WebAddr web 控制台监听地址（默认 127.0.0.1:8088）
	WebAddr string
	// ResumePath 断点续爬状态文件路径（--resume）
	ResumePath string
}

// FuzzConf 常见路径探测配置（Go 侧 http，不开浏览器）
type FuzzConf struct {
	Enable bool   `yaml:"enable"`
	Dict   string `yaml:"dict"`
}

// 保存的格式
type ResultConf struct {
	OutputDir      string
	Format         string
	Name           string
	MergedOutput   string
	Fields         string
	OutputTemplate string
}

// 表单字段级填充规则
type FormConf struct {
	// Rules 按序匹配（正则，对 name/id/placeholder/aria-label 拼串小写匹配），命中即填 value
	Rules []FormFillRule `yaml:"rules"`
	// Skip 命中即不填（验证码类字段默认跳过，避免假值卡住校验）
	Skip []string `yaml:"skip"`
}

// FormFillRule 单条填充规则
type FormFillRule struct {
	Match string `yaml:"match"`
	Value string `yaml:"value"`
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
	// WaitLogin 人工登录模式：强制有头，首页加载后暂停等终端回车再继续爬取。
	// 验证码/短信/扫码等自动化登录搞不定的场景靠它。
	WaitLogin bool `yaml:"wait_login"`
	// PushProxy 被动扫描器地址（xray webscan --listen 等），全部流量经它转发。
	PushProxy string `yaml:"push_proxy"`
	// Engine headless=全浏览器（默认），hybrid=文档页 Go 抓取分流
	Engine string `yaml:"engine"`
	// RateLimit 全局导航限速（每秒最多访问的页面数），0 不限
	RateLimit int `yaml:"rate_limit"`
	// HostRateLimit 单 host 导航限速（每秒），0 不限
	HostRateLimit int `yaml:"host_rate_limit"`
	// Retry 静态请求与 hijack 加载失败的重试次数，默认 0
	Retry int `yaml:"retry"`
}

// 爬取范围规则
type ScopeConf struct {
	CrawlSubdomains bool     `yaml:"crawl_subdomains"`
	Include         []string `yaml:"include"`
	Exclude         []string `yaml:"exclude"`
	SaveOutScope    bool     `yaml:"save_outscope"`
}

// 响应体二次提取：接口与密钥
type ExtractConf struct {
	Enable      bool         `yaml:"enable"`
	Secrets     bool         `yaml:"secrets"`
	SecretRules []SecretRule `yaml:"secret_rules"`
}

// SecretRuleYaml 单条自定义密钥规则
type SecretRule struct {
	Name     string `yaml:"name"`
	Regex    string `yaml:"regex"`
	Severity string `yaml:"severity"`
}

// auto 自动触发的一些参数
type AutoConf struct {
	Slow   float64  `yaml:"slow"`
	Filter []string `yaml:"filter"`
	// MaxClickRepeat 同一个可点击元素最多重复点几次。
	// 多步流程（向导/解锁/结算）需要反复点同一个按钮才会逐步放出后续链接。
	MaxClickRepeat int `yaml:"max_click_repeat"`
	// ShortcutKey 要模拟的键盘快捷键，默认 Ctrl+Shift+S。
	ShortcutKey string `yaml:"shortcut_key"`
	// TabIdle 页面静默多少秒后关闭（进度驱动超时：有新链接就不算静默）。
	// 0 表示用默认值 10s。
	TabIdle int `yaml:"tab_idle"`
	// Spider 有头模式下注入蜘蛛爬行动画叠加层（纯视觉，closed shadow 不影响爬取）。
	Spider bool `yaml:"spider"`
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
	// fix: 原先写成 c.Bool("entrace")，名字拼错导致 --trace 永远读到 false
	trace := c.Bool("trace")
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
	// pprof 调试服务
	pprof := c.Bool("pprof")
	pprofAddr := c.String("pprofaddr")
	// 预置会话 Cookie（可重复传）
	cookies := c.StringSlice("cookie")
	// 限速与重试
	rate := c.Int("rate")
	hostRate := c.Int("hostrate")
	retry := c.Int("retry")
	// scope
	crawlSub := c.Bool("crawlsub")
	scopeInclude := c.StringSlice("scope")
	scopeExclude := c.StringSlice("scopeexclude")
	saveOutScope := c.Bool("outscope")
	// 提取
	extractEnable := c.Bool("extract")
	extractSecrets := c.Bool("secrets")
	// 人工登录 / 扫描器推送 / 引擎
	waitLogin := c.Bool("waitlogin")
	pushProxy := c.String("pushproxy")
	engineMode := c.String("engine")
	// 输出
	outputFields := c.String("fields")
	outputTemplate := c.String("outputtemplate")
	// 路径探测 / 续爬 / web 控制台
	fuzzEnable := c.Bool("fuzz")
	fuzzDict := c.String("fuzzdict")
	resumePath := c.String("resume")
	webConsole := c.Bool("web")
	webAddr := c.String("webaddr")

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
	// 限速与重试（默认 0 = 不限速不重试，行为与历史版本一致）
	if rate != GlobalConfig.BrowserConf.RateLimit {
		GlobalConfig.BrowserConf.RateLimit = rate
	}
	if hostRate != GlobalConfig.BrowserConf.HostRateLimit {
		GlobalConfig.BrowserConf.HostRateLimit = hostRate
	}
	if retry != GlobalConfig.BrowserConf.Retry {
		GlobalConfig.BrowserConf.Retry = retry
	}
	// scope
	if crawlSub != GlobalConfig.ScopeConf.CrawlSubdomains {
		GlobalConfig.ScopeConf.CrawlSubdomains = crawlSub
	}
	if len(scopeInclude) > 0 {
		GlobalConfig.ScopeConf.Include = scopeInclude
	}
	if len(scopeExclude) > 0 {
		GlobalConfig.ScopeConf.Exclude = scopeExclude
	}
	if saveOutScope != GlobalConfig.ScopeConf.SaveOutScope {
		GlobalConfig.ScopeConf.SaveOutScope = saveOutScope
	}
	// 提取（默认 true；显式传 --extract=false 可关闭）
	if extractEnable != GlobalConfig.ExtractConf.Enable {
		GlobalConfig.ExtractConf.Enable = extractEnable
	}
	if extractSecrets != GlobalConfig.ExtractConf.Secrets {
		GlobalConfig.ExtractConf.Secrets = extractSecrets
	}
	// 人工登录 / 扫描器推送 / 引擎模式
	if waitLogin != GlobalConfig.BrowserConf.WaitLogin {
		GlobalConfig.BrowserConf.WaitLogin = waitLogin
	}
	if pushProxy != "" {
		GlobalConfig.BrowserConf.PushProxy = pushProxy
	}
	GlobalConfig.BrowserConf.Engine = engineMode
	// 输出
	GlobalConfig.ResultConf.Fields = outputFields
	GlobalConfig.ResultConf.OutputTemplate = outputTemplate
	// 路径探测 / 续爬
	GlobalConfig.FuzzConf.Enable = fuzzEnable
	GlobalConfig.FuzzConf.Dict = fuzzDict
	GlobalConfig.ResumePath = resumePath
	GlobalConfig.WebConsole = webConsole
	if webAddr != "" {
		GlobalConfig.WebAddr = webAddr
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
	// 进度驱动的页面 idle 超时（0 = 默认 10s）
	tabIdle := c.Int("tabidle")
	if tabIdle != GlobalConfig.AutoConf.TabIdle {
		GlobalConfig.AutoConf.TabIdle = tabIdle
	}
	// 蜘蛛动画叠加层（默认 true，--spider=false 关闭）
	spiderOverlay := c.Bool("spider")
	if spiderOverlay != GlobalConfig.AutoConf.Spider {
		GlobalConfig.AutoConf.Spider = spiderOverlay
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

	// pprof 调试服务（默认关闭）
	GlobalConfig.Pprof = pprof
	GlobalConfig.PprofAddr = pprofAddr
	GlobalConfig.Cookies = cookies

	// 打印最终生效的浏览器配置，方便核对配置文件是否被命令行默认值覆盖
	log.Logger.Debugf("effective browser config: unheadless=%v trace=%v tabcount=%d tabtimeout=%d browsertimeout=%d maxdepth=%d proxy=%q chrome=%q remote=%q user_agent=%q",
		GlobalConfig.BrowserConf.UnHeadless,
		GlobalConfig.BrowserConf.Trace,
		GlobalConfig.BrowserConf.TabCount,
		GlobalConfig.BrowserConf.TabTimeout,
		GlobalConfig.BrowserConf.BrowserTimeout,
		GlobalConfig.BrowserConf.MaxDepth,
		GlobalConfig.BrowserConf.Proxy,
		GlobalConfig.BrowserConf.Chrome,
		GlobalConfig.BrowserConf.Remote,
		GlobalConfig.BrowserConf.UserAgent,
	)
}
