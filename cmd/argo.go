package main

import (
	"argo/pkg/conf"
	"argo/pkg/engine"
	"argo/pkg/log"
	"argo/pkg/req"
	"argo/pkg/updateself"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "net/http/pprof"

	cli "github.com/urfave/cli/v2"
)

var Version = "v1.0"

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
	go func() {
		http.ListenAndServe("0.0.0.0:6060", nil)
	}()
	SetupCloseHandler()
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
			Usage:    "The specified target file list has each target separated by a new line, just like other tools we have used in the past",
			Category: UseArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "unheadless",
			Aliases:  []string{"uh"},
			Value:    false,
			Usage:    "Is the default interface disabled? Specify 'uh' to enable the interface",
			Category: DebugArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "trace",
			Value:    false,
			Usage:    "Whether to display the elements of operation after opening the interface",
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
			Usage:    "If logging in, the default username ",
			Category: DataArgsGroup,
		},
		&cli.StringFlag{
			Name:     "password",
			Aliases:  []string{"p"},
			Value:    "argo123",
			Usage:    "If logging in, the default password",
			Category: DataArgsGroup,
		},
		&cli.StringFlag{
			Name:     "email",
			Value:    "argo@recar.com",
			Usage:    "If logging in, the default email",
			Category: DataArgsGroup,
		},
		&cli.StringFlag{
			Name:     "phone",
			Value:    "18888888888",
			Usage:    "If logging in, the default phone",
			Category: DataArgsGroup,
		},
		&cli.StringFlag{
			Name:     "playback",
			Usage:    "Support replay like headless YAML scripts",
			Category: UseArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "testplayback",
			Usage:    "If opened, then directly end after executing the specified playback script",
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
			Usage:    "Set the maximum running time for the tab, and close the tab if it exceeds the limit. The unit is in seconds",
			Category: ConfigArgsGroup,
		},
		&cli.IntFlag{
			Name:     "browsertimeout",
			Value:    18000,
			Usage:    "Set the maximum running time for the browser, and close the browser if it exceeds the limit. The unit is in seconds",
			Category: ConfigArgsGroup,
		},
		&cli.StringFlag{
			Name:     "save",
			Usage:    "The default name for the saved result is 'target' without a file extension. For example, to save as 'test', use the command '--save test'",
			Category: OutPutArgsGroup,
		},
		&cli.StringFlag{
			Name:     "format",
			Value:    "txt,json",
			Usage:    "Result output format separated by commas, multiple formats can be output at one time, and the supported formats include txt, json, xlsx, and html",
			Category: OutPutArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "debug",
			Value:    false,
			Usage:    "Do you want to output debug information?",
			Category: DebugArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "dev",
			Value:    false,
			Usage:    "Enable dev mode. This will activate the browser interface mode and stop after accessing the page for development and debugging purposes",
			Category: DebugArgsGroup,
		},
		&cli.BoolFlag{
			Name:     "norrs",
			Value:    false,
			Usage:    "There is no storage request response string, which can save memory and is suitable for a large number of scans",
			Category: UseArgsGroup,
		},
		&cli.IntFlag{
			Name:     "maxdepth",
			Value:    10,
			Usage:    "Scrape the web content with increasing depth by crawling each URL based on the last one, and incrementing the current depth by 1 relative to the previous depth. Stop crawling once the maximum depth is reached.",
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
	log.Init(debug)
	log.Logger.Info("[argo start]")
	// 加载/初始化 config.yml
	conf.LoadConfig()
	// 合并 命令行与 yaml
	conf.MergeArgs(c)
	// 浏览器引擎初始化
	for _, t := range conf.GlobalConfig.TargetList {
		log.Logger.Infof("target: %s", t)
		if !req.CheckTarget(t) {
			log.Logger.Errorf("The target is inaccessible %s", t)
			continue
		}
		eif := engine.InitEngine(t)
		eif.Start()

	}
	return nil
}
