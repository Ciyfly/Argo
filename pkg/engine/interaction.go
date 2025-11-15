package engine

import (
	"argo/pkg/conf"
	"argo/pkg/inject"
	"argo/pkg/log"
	"argo/pkg/login"
	"argo/pkg/playback"

	"github.com/go-rod/rod"
)

type InteractionContext struct {
	Engine  *EngineInfo
	Page    *rod.Page
	UrlInfo *UrlInfo
	IsHome  bool
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

func (ei *EngineInfo) runInteractions(page *rod.Page, uif *UrlInfo, isHome bool) []string {
	if len(ei.Interactions) == 0 || page == nil {
		return nil
	}
	ctx := &InteractionContext{
		Engine:  ei,
		Page:    page,
		UrlInfo: uif,
		IsHome:  isHome,
	}
	var collected []string
	for _, inter := range ei.Interactions {
		urls, err := inter.Execute(ctx)
		if err != nil {
			log.Logger.Warnf("interaction %s err: %s", inter.Name(), err)
			continue
		}
		if len(urls) > 0 {
			collected = append(collected, urls...)
		}
	}
	return collected
}

type loginInteraction struct{}

func (l *loginInteraction) Name() string { return "login" }

func (l *loginInteraction) Execute(ctx *InteractionContext) ([]string, error) {
	login.GlobalLoginAutoData.Handler(ctx.Page)
	return nil, nil
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
