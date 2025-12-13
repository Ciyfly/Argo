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
  timeout: 5 # 登录交互阶段超时时间(秒)
browser:
  unheadless: false # 开启则界面
  trace: false # 有界面时显示点击了哪些
  tabcount: 10 # 最多开启多个tab页面
  proxy: ""
  tab_soft_timeout: 60 # Tab 软超时时间，AutoJS 可据此动态续期
  tabtimeout: 240 # Tab 硬超时时间
  browsertimeout: 18000 # 浏览器运行最长时间
  maxdepth: 10 # 爬行最大深度
  disable_leakless: false # 仅当与安全软件冲突时再手动关闭 leakless
  queue_size: 100000 # URL 队列最大长度
  schedule_interval: 0 # 每次调度 Tab 的间隔(ms)，0 表示不限速
auto:
  slow: 1000 # 默认延迟（ms）
  slow_min: 600 # 动作最短等待（ms），<=0 表示禁用随机
  slow_max: 1400 # 动作最长等待（ms），<=slow_min 表示禁用随机
  filter: ["lougout", "登出", "reset"] # 包含这种字符的就不进行触发事件
  action_limit: 200 # 单页自动动作上限
  batch_size: 20 # 每批执行的最大动作数
  command_timeout_ms: 800 # 等待 go 端下一步指令的超时时间(ms)
  interactions: ["login", "playback", "auto"]
  middlewares: ["static", "interaction", "metrics"]
rate_limit:
  enabled: true # 是否启用自适应限速
  base_interval_ms: 200 # 基准间隔(毫秒)，请求之间的最小间隔
  min_interval_ms: 50 # 最小间隔(毫秒)，成功时可降低到此值
  max_interval_ms: 10000 # 最大间隔(毫秒)，被限速时最大等待时间
websocket:
  enabled: true # 是否启用 WebSocket 监控
  max_connections: 100 # 最大追踪连接数
  max_messages_per_conn: 50 # 每个连接最大消息数
  capture_messages: true # 是否捕获消息内容
incremental:
  enabled: false # 是否启用增量爬取
  state_file: "" # 状态文件路径,为空则自动生成
  max_age_hours: 24 # URL最大有效期(小时),超过后重新爬取
  auto_save_interval_min: 5 # 自动保存间隔(分钟)
  resume_from_pending: true # 是否从未完成的URL继续

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
	SeedList         []string
	Dev              bool
	NoReqRspStr      bool
	Quiet            bool
	MetricsFile      string
	SeedOutput       string
	// P1优化: 双引擎模式
	DualEngineConf DualEngineConf `yaml:"dual_engine"`
	// P2优化: 分布式模式
	DistributedMode bool `yaml:"distributed_mode"`
	// P2优化: 自适应限速配置
	RateLimitConf RateLimitConf `yaml:"rate_limit"`
	// P3优化: WebSocket 配置
	WebSocketConf WebSocketConf `yaml:"websocket"`
	// P3优化: 增量爬取配置
	IncrementalConf IncrementalConf `yaml:"incremental"`
}

// DualEngineConf 双引擎配置
type DualEngineConf struct {
	Enabled         bool `yaml:"enabled"`          // 是否启用双引擎
	StandardWorkers int  `yaml:"standard_workers"` // 标准引擎并发数
}

// RateLimitConf 自适应限速配置
type RateLimitConf struct {
	Enabled        bool `yaml:"enabled"`          // 是否启用自适应限速
	BaseIntervalMs int  `yaml:"base_interval_ms"` // 基准间隔(毫秒)
	MinIntervalMs  int  `yaml:"min_interval_ms"`  // 最小间隔(毫秒)
	MaxIntervalMs  int  `yaml:"max_interval_ms"`  // 最大间隔(毫秒)
}

// WebSocketConf WebSocket 配置
type WebSocketConf struct {
	Enabled            bool `yaml:"enabled"`               // 是否启用 WebSocket 监控
	MaxConnections     int  `yaml:"max_connections"`       // 最大追踪连接数
	MaxMessagesPerConn int  `yaml:"max_messages_per_conn"` // 每个连接最大消息数
	CaptureMessages    bool `yaml:"capture_messages"`      // 是否捕获消息内容
}

// IncrementalConf 增量爬取配置
type IncrementalConf struct {
	Enabled             bool   `yaml:"enabled"`                // 是否启用增量爬取
	StateFile           string `yaml:"state_file"`             // 状态文件路径
	MaxAgeHours         int    `yaml:"max_age_hours"`          // URL最大有效期(小时)
	AutoSaveIntervalMin int    `yaml:"auto_save_interval_min"` // 自动保存间隔(分钟)
	ResumeFromPending   bool   `yaml:"resume_from_pending"`    // 是否从未完成的URL继续
}

// 保存的格式
type ResultConf struct {
	OutputDir string
	Format    string
	Name      string
	MQ        MQConf
}

// 默认的用户名密码
type LoginConf struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Email    string `yaml:"email"`
	Phone    string `yaml:"phone"`
	Timeout  int    `yaml:"timeout"`
}

// 浏览器参数
type BrowserConf struct {
	UnHeadless       bool   `yaml:"unheadless"`
	Trace            bool   `yaml:"trace"`
	TabCount         int    `yaml:"tab_count"`
	Proxy            string `yaml:"proxy"`
	TabSoftTimeout   int    `yaml:"tab_soft_timeout"`
	TabTimeout       int    `yaml:"tab_timeout"`
	BrowserTimeout   int    `yaml:"browser_timeout"`
	MaxDepth         int    `yaml:"max_depth"`
	Chrome           string `yaml:"chrome"`
	QueueSize        int    `yaml:"queue_size"`
	ScheduleInterval int    `yaml:"schedule_interval"`
	MaxRetries       int    `yaml:"max_retries"`
	DisableLeakless  bool   `yaml:"disable_leakless"`
	// P0优化: 浏览器池配置
	EnablePool       bool   `yaml:"enable_pool"`        // 是否启用浏览器池
	PoolMaxInstances int    `yaml:"pool_max_instances"` // 池最大实例数
	PoolMaxTabs      int    `yaml:"pool_max_tabs"`      // 每实例最大Tab数
}

// auto 自动触发的一些参数
type AutoConf struct {
	Slow             float64  `yaml:"slow"`
	SlowMin          float64  `yaml:"slow_min"`
	SlowMax          float64  `yaml:"slow_max"`
	Filter           []string `yaml:"filter"`
	ActionLimit      int      `yaml:"action_limit"`
	BatchSize        int      `yaml:"batch_size"`
	CommandTimeoutMs int      `yaml:"command_timeout_ms"`
	Interactions     []string `yaml:"interactions"`
	Middlewares      []string `yaml:"middlewares"`
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
	GlobalConfig.SeedList = make([]string, 0)
}

func MergeArgs(c *cli.Context) {
	target := c.String("target")
	targetsFile := c.String("targetsfile")
	seedFile := c.String("seedfile")
	unheadless := c.Bool("unheadless")
	trace := c.Bool("entrace")
	slow := c.Float64("slow")
	username := c.String("username")
	password := c.String("password")
	loginTimeout := c.Int("logintimeout")
	proxy := c.String("proxy")
	tabCount := c.Int("tabcount")
	tabTimeout := c.Int("tabtimeout")
	browserTimeout := c.Int("browsertimeout")
	chrome := c.String("chrome")
	// 回放
	playback := c.String("playback")
	testPlayback := c.Bool("testplayback")
	// 处理结果参数
	save := c.String("save")
	format := c.String("format")
	outputDir := c.String("outputdir")
	seedOutput := c.String("seedout")
	interactionsArg := c.String("interactions")
	middlewaresArg := c.String("middlewares")
	metricsFile := c.String("metricsfile")

	//静默输出
	quiet := c.Bool("quiet")
	// debug dev
	devMode := c.Bool("dev")

	// 优化控制
	norrs := c.Bool("norrs")
	maxDepth := c.Int("maxdepth")
	queueSize := c.Int("queuesize")
	scheduleInterval := c.Int("scheduleinterval")
	maxRetries := c.Int("maxretries")

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
	if seedFile != "" {
		if err := loadSeedFile(seedFile); err != nil {
			log.Logger.Warnf("seedfile load err: %s", err)
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
	if tabTimeout > 0 {
		GlobalConfig.BrowserConf.TabTimeout = tabTimeout
	}
	tabSoftTimeout := c.Int("tabsofttimeout")
	if tabSoftTimeout > 0 {
		GlobalConfig.BrowserConf.TabSoftTimeout = tabSoftTimeout
	}
	if browserTimeout != GlobalConfig.BrowserConf.BrowserTimeout {
		GlobalConfig.BrowserConf.BrowserTimeout = browserTimeout
	}
	if chrome != GlobalConfig.BrowserConf.Chrome {
		GlobalConfig.BrowserConf.Chrome = chrome
	}
	if queueSize != 0 {
		GlobalConfig.BrowserConf.QueueSize = queueSize
	}
	if scheduleInterval != 0 {
		GlobalConfig.BrowserConf.ScheduleInterval = scheduleInterval
	}
	// 登录参数
	if username != GlobalConfig.LoginConf.Username {
		GlobalConfig.LoginConf.Username = username
	}
	if password != GlobalConfig.LoginConf.Password {
		GlobalConfig.LoginConf.Password = password
	}
	if loginTimeout > 0 && loginTimeout != GlobalConfig.LoginConf.Timeout {
		GlobalConfig.LoginConf.Timeout = loginTimeout
	}
	// auto
	if slow != GlobalConfig.AutoConf.Slow {
		GlobalConfig.AutoConf.Slow = slow
	}
	if slowMin := c.Float64("slowmin"); slowMin > 0 {
		GlobalConfig.AutoConf.SlowMin = slowMin
	}
	if slowMax := c.Float64("slowmax"); slowMax > 0 {
		GlobalConfig.AutoConf.SlowMax = slowMax
	}
	// playback
	GlobalConfig.PlaybackPath = playback
	GlobalConfig.TestPlayBack = testPlayback
	// 结果处理参数
	GlobalConfig.ResultConf.Name = save
	GlobalConfig.ResultConf.Format = format
	GlobalConfig.ResultConf.OutputDir = outputDir
	GlobalConfig.SeedOutput = seedOutput
	GlobalConfig.MetricsFile = metricsFile
	if interactionsArg != "" {
		GlobalConfig.AutoConf.Interactions = parseList(interactionsArg)
	}
	if middlewaresArg != "" {
		GlobalConfig.AutoConf.Middlewares = parseList(middlewaresArg)
	}

	//dev
	GlobalConfig.Dev = devMode

	// 静默
	GlobalConfig.Quiet = quiet

	// 优化控制
	GlobalConfig.NoReqRspStr = norrs
	GlobalConfig.BrowserConf.MaxDepth = maxDepth
	if maxRetries != 0 {
		GlobalConfig.BrowserConf.MaxRetries = maxRetries
	}

	// 自动交互默认值兜底
	if GlobalConfig.AutoConf.ActionLimit <= 0 {
		GlobalConfig.AutoConf.ActionLimit = 200
	}
	if GlobalConfig.AutoConf.BatchSize <= 0 {
		GlobalConfig.AutoConf.BatchSize = 20
	}
	if GlobalConfig.AutoConf.CommandTimeoutMs <= 0 {
		GlobalConfig.AutoConf.CommandTimeoutMs = 800
	}
	if GlobalConfig.AutoConf.SlowMin < 0 {
		GlobalConfig.AutoConf.SlowMin = 0
	}
	if GlobalConfig.AutoConf.SlowMax < 0 {
		GlobalConfig.AutoConf.SlowMax = 0
	}
	if GlobalConfig.AutoConf.SlowMax > 0 && GlobalConfig.AutoConf.SlowMin > 0 && GlobalConfig.AutoConf.SlowMax <= GlobalConfig.AutoConf.SlowMin {
		GlobalConfig.AutoConf.SlowMax = 0
	}

	if GlobalConfig.BrowserConf.TabTimeout <= 0 {
		GlobalConfig.BrowserConf.TabTimeout = 180
	}
	if GlobalConfig.BrowserConf.TabSoftTimeout <= 0 || GlobalConfig.BrowserConf.TabSoftTimeout > GlobalConfig.BrowserConf.TabTimeout {
		GlobalConfig.BrowserConf.TabSoftTimeout = GlobalConfig.BrowserConf.TabTimeout
	}

}

func parseList(input string) []string {
	parts := strings.Split(input, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trim := strings.TrimSpace(p)
		if trim != "" {
			result = append(result, trim)
		}
	}
	return result
}

func loadSeedFile(seedPath string) error {
	if !utils.IsExist(seedPath) {
		return fmt.Errorf("seedfile not exist: %s", seedPath)
	}
	file, err := os.Open(seedPath)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		GlobalConfig.SeedList = append(GlobalConfig.SeedList, line)
	}
	return scanner.Err()
}

type MQConf struct {
	Type      string `yaml:"type"`
	Address   string `yaml:"address"`
	QueueName string `yaml:"queue"`
}
