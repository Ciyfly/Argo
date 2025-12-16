package engine

import (
	"argo/pkg/static"
	"github.com/go-rod/rod"
	"time"
)

type PageContext struct {
	Engine        *EngineInfo
	Page          *rod.Page
	Url           *UrlInfo
	PageFlag      int
	StageRecorder func(string) // StageRecorder 用于在关键步骤更新 stage 以便定位超时
	ExtendTimeout func(time.Duration)
}

type PageMiddleware interface {
	Name() string
	Handle(ctx *PageContext) error
}

type middlewareChain struct {
	middlewares []PageMiddleware
}

type middlewareFactory func() PageMiddleware

var middlewareRegistry = map[string]middlewareFactory{}

func RegisterPageMiddleware(name string, factory middlewareFactory) {
	if name == "" || factory == nil {
		return
	}
	middlewareRegistry[name] = factory
}

func newMiddlewareChain(m []PageMiddleware) *middlewareChain {
	return &middlewareChain{middlewares: m}
}

func (mc *middlewareChain) Execute(ctx *PageContext) error {
	for _, m := range mc.middlewares {
		if err := m.Handle(ctx); err != nil {
			return err
		}
	}
	return nil
}

// default middlewares

type staticParseMiddleware struct{}

type interactionMiddleware struct{}

type metricsMiddleware struct{}

func (s *staticParseMiddleware) Name() string { return "static" }

func (s *staticParseMiddleware) Handle(ctx *PageContext) error {
	discovered := static.ParseDomWithSource(ctx.Page)
	for _, item := range discovered {
		if item.URL == "" {
			continue
		}
		sourceType := item.SourceType
		if sourceType == "" {
			sourceType = SourceTypeHTMLAttr
		}
		ctx.Engine.PushStaticUrl(&UrlInfo{Url: item.URL, SourceType: sourceType, SourceUrl: ctx.Url.Url, Depth: ctx.Url.Depth + 1})
		ctx.Engine.EnrichStaticResource(item.URL, ctx.Url.Depth+1)
	}
	return nil
}

func (i *interactionMiddleware) Name() string { return "interaction" }

func (i *interactionMiddleware) Handle(ctx *PageContext) error {
	if ctx == nil {
		return nil
	}
	if ctx.StageRecorder != nil {
		ctx.StageRecorder("interaction_chain:start")
	}
	discovered := ctx.Engine.runInteractions(ctx.Page, ctx.Url, ctx.PageFlag == HOME_PAGE_FLAG, ctx.StageRecorder, ctx.ExtendTimeout)
	for _, item := range discovered {
		if item.URL == "" {
			continue
		}
		sourceType := item.SourceType
		if sourceType == "" {
			sourceType = SourceTypeInteractionAuto
		}
		ctx.Engine.PushStaticUrl(&UrlInfo{Url: item.URL, SourceType: sourceType, SourceUrl: ctx.Url.Url, Depth: ctx.Url.Depth + 1})
	}
	if ctx.StageRecorder != nil {
		ctx.StageRecorder("interaction_chain:done")
	}
	return nil
}

func (m *metricsMiddleware) Name() string { return "metrics" }

func (m *metricsMiddleware) Handle(ctx *PageContext) error {
	ctx.Engine.RecordPageProcessed(ctx.Url)
	return nil
}

func init() {
	RegisterPageMiddleware("static", func() PageMiddleware { return &staticParseMiddleware{} })
	RegisterPageMiddleware("interaction", func() PageMiddleware { return &interactionMiddleware{} })
	RegisterPageMiddleware("metrics", func() PageMiddleware { return &metricsMiddleware{} })
}
