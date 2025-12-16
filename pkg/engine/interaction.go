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
	ExtendTimeout func(time.Duration)
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

type InteractionDiscoveredURL struct {
	URL        string
	SourceType string
}

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

// reorderInteractions 按优先级数组排序，缺失的按原顺序追加
func reorderInteractions(list []Interaction, priority []string) []Interaction {
	nameIndex := map[string]Interaction{}
	for _, inter := range list {
		nameIndex[inter.Name()] = inter
	}
	ordered := []Interaction{}
	exists := map[string]bool{}
	for _, name := range priority {
		if inter, ok := nameIndex[name]; ok {
			ordered = append(ordered, inter)
			exists[name] = true
		}
	}
	for _, inter := range list {
		if !exists[inter.Name()] {
			ordered = append(ordered, inter)
		}
	}
	return ordered
}

func (ei *EngineInfo) runInteractions(page *rod.Page, uif *UrlInfo, isHome bool, stageRecorder func(string), extendTimeout func(time.Duration)) []InteractionDiscoveredURL {
	if len(ei.Interactions) == 0 || page == nil {
		return nil
	}
	ctx := &InteractionContext{
		Engine:        ei,
		Page:          page,
		UrlInfo:       uif,
		IsHome:        isHome,
		StageRecorder: stageRecorder,
		ExtendTimeout: extendTimeout,
	}

	priority := []string{"auto", "login", "playback"}
	if conf.GlobalConfig.PlaybackPath != "" {
		priority = []string{"playback", "auto", "login"}
	}
	runOrder := reorderInteractions(ei.Interactions, priority)

	var collected []InteractionDiscoveredURL
	for _, inter := range runOrder {
		if stageRecorder != nil {
			stageRecorder("interaction:" + inter.Name())
		}
		urls, err := inter.Execute(ctx)
		if err != nil {
			log.Logger.Warnf("interaction %s err: %s", inter.Name(), err)
			continue
		}
		if len(urls) > 0 {
			sourceType := "interaction_" + inter.Name()
			switch inter.Name() {
			case "auto":
				sourceType = SourceTypeInteractionAuto
			case "login":
				sourceType = SourceTypeInteractionLogin
			case "playback":
				sourceType = SourceTypeInteractionPlayback
			}
			for _, u := range urls {
				if u == "" {
					continue
				}
				collected = append(collected, InteractionDiscoveredURL{URL: u, SourceType: sourceType})
			}
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
	if ctx == nil || ctx.Page == nil || ctx.Engine == nil {
		return nil, nil
	}
	executed := false
	ctx.Engine.loginOnce.Do(func() {
		executed = true
		ctx.Engine.loginOnceErr = l.performLogin(ctx)
	})
	if !executed {
		log.Logger.Debug("login interaction already attempted once, skip")
		if ctx.StageRecorder != nil {
			ctx.StageRecorder("interaction:login:skipped")
		}
		return nil, ctx.Engine.loginOnceErr
	}
	return nil, ctx.Engine.loginOnceErr
}

func (l *loginInteraction) performLogin(ctx *InteractionContext) error {
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
		return nil
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
		return fmt.Errorf("login interaction timeout after %ds", timeoutSec)
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
	extend := func(d time.Duration) {}
	if ctx != nil && ctx.ExtendTimeout != nil {
		extend = ctx.ExtendTimeout
		extend(inject.EstimateAutoDuration())
	}
	urls := inject.Auto(ctx.Page, extend)
	return urls, nil
}
