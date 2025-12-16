package engine

// URL 来源类型（SourceType）约定：
// - 用于描述“这个 URL 是从哪里发现/产生的”
// - 与“最终由哪个引擎处理(standard/hybrid)”是两件事
//
// 说明：历史代码里 SourceType 是自由字符串，这里定义一组稳定枚举供新逻辑使用；
//
//	旧值仍可继续存在（只要不影响调度逻辑）。
const (
	// 入口/种子
	SourceTypeHomePage = "homePage" // 目标入口（注意：调度器里有特殊处理）
	SourceTypeSeed     = "seed"     // 种子文件/命令行 seed
	SourceTypeMetadata = "metadata" // robots/sitemap 等元数据
	// 增量恢复
	SourceTypeIncrementalResume = "incremental_resume" // 增量爬取断点恢复

	// 静态解析（HTML/JS/JSON）
	SourceTypeHTMLAttr    = "html_attr"    // HTML 标签属性(href/src/action/srcset/meta refresh等)
	SourceTypeHTMLText    = "html_text"    // HTML 文本中匹配到的 URL
	SourceTypeHTMLComment = "html_comment" // HTML 注释中匹配到的 URL
	SourceTypeHTMLForm    = "html_form"    // 表单(action/method) 生成的 URL
	SourceTypeJSInline    = "js_inline"    // HTML 内联 <script> 提取
	SourceTypeJSFile      = "js_file"      // 外部 JS 文件内容解析提取
	SourceTypeJSON        = "json"         // JSON 内容解析提取

	// 交互/动态
	SourceTypeInteractionAuto     = "interaction_auto"
	SourceTypeInteractionLogin    = "interaction_login"
	SourceTypeInteractionPlayback = "interaction_playback"
	SourceTypeBrowserHijack       = "browser_hijack" // 浏览器运行时劫持到的请求
	SourceTypePatch               = "patch"          // 动态交互后“当前URL”补推（处理 SPA/History 导航）

	// 其他发现模块
	SourceTypeSwagger       = "swagger"
	SourceTypeGraphQL       = "graphql"
	SourceTypePassiveSource = "passive_source"
	SourceTypePathClimb     = "path_climb"
	SourceTypeResource      = "resource" // 静态资源二次解析/增强发现
)
