package engine

import (
	"fmt"
	"time"

	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/playback"

	"github.com/go-rod/rod"
)

type InteractionContext struct {
	Engine        *EngineInfo
	Page          *rod.Page
	UrlInfo       *UrlInfo
	IsHome        bool
	StageRecorder func(string)
}

type Interaction interface {
	Name() string
	Execute(ctx *InteractionContext) ([]string, error)
}

type interactionFactory func() Interaction

var interactionRegistry = map[string]interactionFactory{
	"login":    func() Interaction { return &loginInteraction{} },
	"playback": func() Interaction { return &playbackInteraction{} },
	"auto":     func() Interaction { return &autoInteraction{} },
}

const defaultLoginInteractionTimeoutSeconds = 5

func (ei *EngineInfo) InitInteractions() {
	order := conf.GlobalConfig.AutoConf.Interactions
	if len(order) == 0 {
		order = []string{"login", "playback", "auto"}
	}
	for _, name := range order {
		if factory, ok := interactionRegistry[name]; ok {
			ei.Interactions = append(ei.Interactions, factory())
		} else {
			log.Logger.Warnf("interaction %s not found", name)
		}
	}
}

func (ei *EngineInfo) runInteractions(page *rod.Page, uif *UrlInfo, isHome bool, stageRecorder func(string)) []string {
	if len(ei.Interactions) == 0 || page == nil {
		return nil
	}
	ctx := &InteractionContext{
		Engine:        ei,
		Page:          page,
		UrlInfo:       uif,
		IsHome:        isHome,
		StageRecorder: stageRecorder,
	}
	var collected []string
	for _, inter := range ei.Interactions {
		if stageRecorder != nil {
			stageRecorder("interaction:" + inter.Name())
		}
		urls, err := inter.Execute(ctx)
		if err != nil {
			log.Logger.Warnf("interaction %s err: %s", inter.Name(), err)
			continue
		}
		if len(urls) > 0 {
			collected = append(collected, urls...)
		}
		if stageRecorder != nil {
			stageRecorder("interaction:" + inter.Name() + ":done")
		}
	}
	return collected
}

type loginInteraction struct{}

func (l *loginInteraction) Name() string { return "login" }

func (l *loginInteraction) Execute(ctx *InteractionContext) ([]string, error) {
	if ctx == nil || ctx.Page == nil {
		return nil, nil
	}

	timeoutSec := conf.GlobalConfig.LoginConf.Timeout
	if timeoutSec <= 0 {
		timeoutSec = defaultLoginInteractionTimeoutSeconds
	}
	timeout := time.Duration(timeoutSec) * time.Second
	timeoutPage := ctx.Page.Timeout(timeout)
	done := make(chan struct{}, 1)
	go func() {
		login.GlobalLoginAutoData.Handler(timeoutPage, ctx.StageRecorder)
		done <- struct{}{}
	}()

	select {
	case <-done:
		timeoutPage.CancelTimeout()
		if ctx.StageRecorder != nil {
			ctx.StageRecorder("interaction:login:success")
		}
		return nil, nil
	case <-time.After(timeout):
		timeoutPage.CancelTimeout()
		if ctx.StageRecorder != nil {
			ctx.StageRecorder("interaction:login:timeout")
		}
		target := ""
		if ctx.UrlInfo != nil {
			target = ctx.UrlInfo.Url
		}
		log.Logger.Warnf("login interaction timeout (%ds): %s", timeoutSec, target)
		return nil, fmt.Errorf("login interaction timeout after %ds", timeoutSec)
	}
}

type playbackInteraction struct{}

func (p *playbackInteraction) Name() string { return "playback" }

func (p *playbackInteraction) Execute(ctx *InteractionContext) ([]string, error) {
	if !ctx.IsHome || conf.GlobalConfig.PlaybackPath == "" {
		return nil, nil
	}
	playback.Run(conf.GlobalConfig.PlaybackPath, ctx.Page)
	return nil, nil
}

type autoInteraction struct{}

func (a *autoInteraction) Name() string { return "auto" }

func (a *autoInteraction) Execute(ctx *InteractionContext) ([]string, error) {
	urls := inject.Auto(ctx.Page)
	return urls, nil
}
