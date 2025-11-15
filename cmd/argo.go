package main

import (
	"encoding/json"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"argo/pkg/conf"
	"argo/pkg/engine"
	"argo/pkg/log"
	"argo/pkg/req"
	"argo/pkg/updateself"
	"fmt"

	cli "github.com/urfave/cli/v2"
)

var Version = "v1.0"

var metricsRegistry = struct {
	sync.RWMutex
	data map[string]engine.MetricsSummary
}{data: make(map[string]engine.MetricsSummary)}

func SetupCloseHandler() {
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, os.Interrupt, os.Kill, syscall.SIGKILL)
	go func() {
		<-c
		fmt.Println("ctrl+c exit")
		os.Exit(0)
	}()
}

const (
	UseArgsGroup     string = "Use"
	BrowserArgsGroup        = "Browser"
	DataArgsGroup           = "Data"
	ConfigArgsGroup         = "Config"
	OutPutArgsGroup         = "OutPut"
	DebugArgsGroup          = "Debug"
	UpdateArgsGroup         = "Update"
)

func main() {
	// go func() {
	// 	http.ListenAndServe("0.0.0.0:6060", nil)
	// }()
	SetupCloseHandler()
	http.HandleFunc("/metrics", metricsHandler)
	app := cli.NewApp()
	app.Name = "argo"
	app.Authors = []*cli.Author{&cli.Author{Name: "Recar", Email: "https://github.com/Ciyfly"}}
	app.Usage = " -t http://testphp.vulnweb.com/"
	app.Version = Version

	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:     "target",
			Aliases:  []string{"t"},
			Value:    "",
			Usage:    "Specify the entry point for testing",
			Category: UseArgsGroup,
		},
		&cli.StringFlag{
			Name:     "targetsfile",
			Aliases:  []string{"f"},
			Value:    "",
			Usage:    "The file list has targets separated by new lines, like other tools we've used before.",
			Category: UseArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "unheadless",
			Aliases:  []string{"uh"},
			Value:    false,
			Usage:    "Default interface disabled? Use 'uh' to enable it.",
			Category: DebugArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "trace",
			Value:    false,
			Usage:    "Display operation elements after interface opens?",
			Category: BrowserArgsGroup,
		},
		&cli.Float64Flag{
			Name:     "slow",
			Value:    1000,
			Usage:    "The default delay time for operating after enabling ",
			Category: BrowserArgsGroup,
		},
		&cli.StringFlag{
			Name:     "username",
			Aliases:  []string{"u"},
			Value:    "argo",
			Usage:    "Default username if logging in.",
			Category: DataArgsGroup,
		},
		&cli.StringFlag{
			Name:     "password",
			Aliases:  []string{"p"},
			Value:    "argo123",
			Usage:    "Default password if logging in.",
			Category: DataArgsGroup,
		},
		&cli.StringFlag{
			Name:     "email",
			Value:    "argo@recar.com",
			Usage:    "Default email if logging in.",
			Category: DataArgsGroup,
		},
		&cli.StringFlag{
			Name:     "phone",
			Value:    "18888888888",
			Usage:    "Default phone if logging in.",
			Category: DataArgsGroup,
		},
		&cli.IntFlag{
			Name:     "logintimeout",
			Value:    5,
			Usage:    "Max seconds to wait for the login interaction before giving up.",
			Category: DataArgsGroup,
		},
		&cli.StringFlag{
			Name:     "playback",
			Usage:    "Support replay like headless YAML scripts",
			Category: UseArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "testplayback",
			Usage:    "irectly end if open, after specified playback script execution.",
			Category: DebugArgsGroup,
		},
		&cli.StringFlag{
			Name:     "proxy",
			Value:    "",
			Usage:    "Set up a proxy, for example, http://127.0.0.1:3128",
			Category: UseArgsGroup,
		},
		&cli.IntFlag{
			Name:     "tabcount",
			Aliases:  []string{"c"},
			Value:    10,
			Usage:    "The maximum number of tab pages that can be opened",
			Category: ConfigArgsGroup,
		},
		&cli.IntFlag{
			Name:     "tabtimeout",
			Value:    30,
			Usage:    "Set max tab run time, close if limit exceeded. Unit is seconds.",
			Category: ConfigArgsGroup,
		},
		&cli.IntFlag{
			Name:     "maxretries",
			Value:    2,
			Usage:    "Number of times to retry a timed-out tab before giving up.",
			Category: ConfigArgsGroup,
		},
		&cli.IntFlag{
			Name:     "browsertimeout",
			Value:    3600,
			Usage:    "Set max browser run time, close if limit exceeded. Unit is seconds.",
			Category: ConfigArgsGroup,
		},
		&cli.StringFlag{
			Name:     "chrome",
			Value:    "",
			Usage:    "Specify the Chrome executable path, e.g. --chrome /opt/google/chrome/chrome",
			Category: ConfigArgsGroup,
		},
		&cli.IntFlag{
			Name:     "queuesize",
			Value:    100000,
			Usage:    "Maximum pending URL queue length before dropping.",
			Category: ConfigArgsGroup,
		},
		&cli.IntFlag{
			Name:     "scheduleinterval",
			Value:    0,
			Usage:    "Interval in milliseconds between launching new tabs (0 for unlimited).",
			Category: ConfigArgsGroup,
		},
		&cli.StringFlag{
			Name:     "save",
			Usage:    "Result saved as 'target' by default. Use '--save test' to save as 'test'.",
			Category: OutPutArgsGroup,
		},
		&cli.StringFlag{
			Name:     "outputdir",
			Usage:    "save output to directory",
			Category: OutPutArgsGroup,
		},
		&cli.StringFlag{
			Name:     "metricsfile",
			Usage:    "write crawl metrics summary to this JSON file",
			Category: OutPutArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "quiet",
			Usage:    "Enable quiet mode to output only the URL information that has been retrieved, in JSON format",
			Category: OutPutArgsGroup,
		},

		&cli.StringFlag{
			Name:     "format",
			Value:    "txt,json",
			Usage:    "Output formats separated by commas, e.g. txt,json,xlsx,html,jsonl.",
			Category: OutPutArgsGroup,
		},
		&cli.StringFlag{
			Name:     "interactions",
			Usage:    "Comma separated interaction plugins order",
			Category: ConfigArgsGroup,
		},
		&cli.StringFlag{
			Name:     "middlewares",
			Usage:    "Comma separated page middleware order",
			Category: ConfigArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "debug",
			Value:    false,
			Usage:    "Output debug info?",
			Category: DebugArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "dev",
			Value:    false,
			Usage:    "Enable dev mode, activates browser interface and stops after page access for dev purposes.",
			Category: DebugArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "norrs",
			Value:    false,
			Usage:    "No storage of req-res strings, saves memory, suitable for large scans.",
			Category: UseArgsGroup,
		},
		&cli.IntFlag{
			Name:     "maxdepth",
			Value:    10,
			Usage:    "Scrape web content with increasing depth by crawling URLs, stop at max depth.",
			Category: ConfigArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "update",
			Value:    false,
			Usage:    "update self",
			Category: UpdateArgsGroup,
		},
	}
	app.Action = RunMain

	err := app.Run(os.Args)
	if err != nil {
		fmt.Printf("cli.RunApp err: %s\n", err.Error())
		return
	}
}

func RunMain(c *cli.Context) error {
	update := c.Bool("update")
	if update {
		updateself.CheckIfUpgradeRequired(Version)
		return nil
	}
	target := c.String("target")
	targetsFile := c.String("targetsfile")
	if target == "" && targetsFile == "" {
		fmt.Println("you need input target or targetsfile -h look look")
		os.Exit(1)
	}
	debug := c.Bool("debug")
	quiet := c.Bool("quiet")
	log.Init(debug, quiet)
	log.Logger.Info("[argo start]")
	// 加载/初始化 config.yml
	conf.LoadConfig()
	// 合并 命令行与 yaml
	conf.MergeArgs(c)
	// 浏览器引擎初始化
	go func() {
		http.ListenAndServe("0.0.0.0:5208", nil)
	}()
	var wg sync.WaitGroup
	errChan := make(chan error, len(conf.GlobalConfig.TargetList))
	metricsChan := make(chan engine.MetricsSummary, len(conf.GlobalConfig.TargetList))
	for _, t := range conf.GlobalConfig.TargetList {
		target := t
		log.Logger.Infof("target: %s", target)
		if !req.CheckTarget(target) {
			log.Logger.Errorf("The target is inaccessible %s", target)
			continue
		}
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			eif := engine.InitEngine(target)
			if err := eif.Start(); err != nil {
				errChan <- fmt.Errorf("target %s: %w", target, err)
			}
			summary := eif.MetricsSummary()
			recordMetrics(summary)
			metricsChan <- summary
		}(target)
	}
	wg.Wait()
	close(errChan)
	close(metricsChan)
	var firstErr error
	for err := range errChan {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	metrics := make([]engine.MetricsSummary, 0, len(conf.GlobalConfig.TargetList))
	for m := range metricsChan {
		metrics = append(metrics, m)
	}
	if conf.GlobalConfig.MetricsFile != "" {
		if err := writeMetricsFile(conf.GlobalConfig.MetricsFile, metrics); err != nil {
			log.Logger.Errorf("write metrics file err: %s", err)
		}
	}
	return firstErr
}

func writeMetricsFile(path string, data []engine.MetricsSummary) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0644)
}

func recordMetrics(summary engine.MetricsSummary) {
	metricsRegistry.Lock()
	metricsRegistry.data[summary.Target] = summary
	metricsRegistry.Unlock()
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	metricsRegistry.RLock()
	resp := make([]engine.MetricsSummary, 0, len(metricsRegistry.data))
	for _, v := range metricsRegistry.data {
		resp = append(resp, v)
	}
	metricsRegistry.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
}
