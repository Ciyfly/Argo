package engine

import (
	"argo/pkg/static"
	"github.com/go-rod/rod"
)

type PageContext struct {
	Engine   *EngineInfo
	Page     *rod.Page
	Url      *UrlInfo
	PageFlag int
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
	staticUrls := static.ParseDom(ctx.Page)
	if staticUrls != nil {
		for _, staticUrl := range staticUrls {
			ctx.Engine.PushStaticUrl(&UrlInfo{Url: staticUrl, SourceType: "static parse", SourceUrl: ctx.Url.Url, Depth: ctx.Url.Depth + 1})
		}
	}
	return nil
}

func (i *interactionMiddleware) Name() string { return "interaction" }

func (i *interactionMiddleware) Handle(ctx *PageContext) error {
	urls := ctx.Engine.runInteractions(ctx.Page, ctx.Url, ctx.PageFlag == HOME_PAGE_FLAG)
	for _, u := range urls {
		ctx.Engine.PushStaticUrl(&UrlInfo{Url: u, SourceType: "interaction", SourceUrl: ctx.Url.Url, Depth: ctx.Url.Depth + 1})
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
