package main

import (
	"argo/pkg/conf"
	"argo/pkg/engine"
	"argo/pkg/log"
	"argo/pkg/req"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cli "github.com/urfave/cli/v2"
)

var Version = "1.0"

func SetupCloseHandler() {
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, os.Interrupt, os.Kill, syscall.SIGKILL)
	go func() {
		<-c
		fmt.Println("ctrl+c exit")
		os.Exit(0)
	}()
}

func main() {
	SetupCloseHandler()
	app := cli.NewApp()
	app.Name = "argo"
	app.Authors = []*cli.Author{&cli.Author{Name: "Recar", Email: "https://github.com/Ciyfly"}}
	app.Usage = " -t http://testphp.vulnweb.com/"
	app.Version = Version

	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "target",
			Aliases: []string{"t"},
			Value:   "",
			Usage:   "Specify the entry point for testing",
		},
		&cli.StringFlag{
			Name:    "targetsfile",
			Aliases: []string{"f"},
			Value:   "",
			Usage:   "The specified target file list has each target separated by a new line, just like other tools we have used in the past",
		},
		&cli.BoolFlag{
			Name:    "unheadless",
			Aliases: []string{"uh"},
			Value:   false,
			Usage:   "Is the default interface disabled? Specify 'uh' to enable the interface",
		},
		&cli.BoolFlag{
			Name:  "trace",
			Value: false,
			Usage: "Whether to display the elements of operation after opening the interface",
		},
		&cli.Float64Flag{
			Name:  "slow",
			Value: 1000,
			Usage: "The default delay time for operating after enabling ",
		},
		&cli.StringFlag{
			Name:    "username",
			Aliases: []string{"u"},
			Value:   "argo",
			Usage:   "If logging in, the default username ",
		},
		&cli.StringFlag{
			Name:    "password",
			Aliases: []string{"p"},
			Value:   "argo123",
			Usage:   "If logging in, the default password",
		},
		&cli.StringFlag{
			Name:  "email",
			Value: "argo@recar.com",
			Usage: "If logging in, the default email",
		},
		&cli.StringFlag{
			Name:  "phone",
			Value: "18888888888",
			Usage: "If logging in, the default phone",
		},
		&cli.StringFlag{
			Name:  "playback",
			Usage: "Support replay like headless YAML scripts",
		},
		&cli.BoolFlag{
			Name:  "testplayback",
			Usage: "If opened, then directly end after executing the specified playback script",
		},
		&cli.StringFlag{
			Name:  "proxy",
			Value: "",
			Usage: "Set up a proxy, for example, http://127.0.0.1:3128",
		},
		&cli.IntFlag{
			Name:    "tabcount",
			Aliases: []string{"c"},
			Value:   10,
			Usage:   "The maximum number of tab pages that can be opened",
		},
		&cli.IntFlag{
			Name:  "tabtimeout",
			Value: 30,
			Usage: "Set the maximum running time for the tab, and close the tab if it exceeds the limit. The unit is in seconds",
		},
		&cli.IntFlag{
			Name:  "browsertimeout",
			Value: 18000,
			Usage: "Set the maximum running time for the browser, and close the browser if it exceeds the limit. The unit is in seconds",
		},
		&cli.StringFlag{
			Name:  "save",
			Usage: "The default name for the saved result is 'target' without a file extension. For example, to save as 'test', use the command '--save test'",
		},
		&cli.StringFlag{
			Name:  "format",
			Value: "txt,json",
			Usage: "Result output format separated by commas, multiple formats can be output at one time, and the supported formats include txt, json, xlsx, and html",
		},
		&cli.BoolFlag{
			Name:  "debug",
			Value: false,
			Usage: "Do you want to output debug information?",
		},
		&cli.BoolFlag{
			Name:  "dev",
			Value: false,
			Usage: "Enable dev mode. This will activate the browser interface mode and stop after accessing the page for development and debugging purposes",
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
