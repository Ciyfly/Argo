package engine

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"argo/pkg/conf"
	"argo/pkg/log"
)

// SmartDepthController 智能深度控制器
type SmartDepthController struct {
	mu              sync.RWMutex
	baseMaxDepth    int                       // 基础最大深度
	pageScores      map[string]*PageScore     // 页面评分缓存
	domainStats     map[string]*DomainStats   // 域名统计
	pathPatterns    map[string]*PathPattern   // 路径模式
	config          *SmartDepthConfig

	// 统计
	depthAdjustments int64
	highValuePages   int64
	lowValuePages    int64
}

// SmartDepthConfig 配置
type SmartDepthConfig struct {
	BaseMaxDepth       int     // 基础最大深度
	MinDepth           int     // 最小深度
	MaxDepth           int     // 绝对最大深度
	HighValueThreshold float64 // 高价值页面阈值
	LowValueThreshold  float64 // 低价值页面阈值
	EnableLearning     bool    // 启用学习模式
}

// PageScore 页面评分
type PageScore struct {
	URL           string
	Score         float64
	Factors       map[string]float64
	AdjustedDepth int
	LinksFound    int
	FormsFound    int
	APICallsFound int
}

// DomainStats 域名统计
type DomainStats struct {
	Domain         string
	TotalPages     int64
	HighValueCount int64
	LowValueCount  int64
	AvgScore       float64
	CommonPaths    map[string]int
}

// PathPattern 路径模式
type PathPattern struct {
	Pattern     string
	Regex       *regexp.Regexp
	ScoreModifier float64 // 分数修正值
	DepthBonus    int     // 深度加成
	Description   string
}

// 默认高价值路径模式
var defaultHighValuePatterns = []*PathPattern{
	// 后台/管理
	{Pattern: `/(admin|manage|backend|dashboard|console)`, ScoreModifier: 0.3, DepthBonus: 2, Description: "管理后台"},
	{Pattern: `/(cms|control|panel)`, ScoreModifier: 0.25, DepthBonus: 2, Description: "控制面板"},

	// API相关
	{Pattern: `/(api|rest|graphql|v[0-9]+)(/|$)`, ScoreModifier: 0.35, DepthBonus: 3, Description: "API端点"},
	{Pattern: `/swagger|/docs/api|/api-docs`, ScoreModifier: 0.4, DepthBonus: 2, Description: "API文档"},

	// 用户功能
	{Pattern: `/(user|account|profile|member|auth)`, ScoreModifier: 0.2, DepthBonus: 1, Description: "用户功能"},
	{Pattern: `/(login|signin|register|signup|logout)`, ScoreModifier: 0.25, DepthBonus: 1, Description: "认证页面"},
	{Pattern: `/(password|reset|forgot|recover)`, ScoreModifier: 0.2, DepthBonus: 1, Description: "密码相关"},

	// 数据操作
	{Pattern: `/(upload|download|export|import)`, ScoreModifier: 0.3, DepthBonus: 2, Description: "数据操作"},
	{Pattern: `/(file|attachment|media)`, ScoreModifier: 0.15, DepthBonus: 1, Description: "文件管理"},

	// 配置/设置
	{Pattern: `/(config|setting|preference|option)`, ScoreModifier: 0.25, DepthBonus: 2, Description: "配置设置"},

	// 支付/订单
	{Pattern: `/(pay|payment|checkout|order|cart)`, ScoreModifier: 0.3, DepthBonus: 2, Description: "支付订单"},

	// 搜索
	{Pattern: `/(search|query|find)`, ScoreModifier: 0.15, DepthBonus: 1, Description: "搜索功能"},

	// 动态内容
	{Pattern: `\?.*action=|do=|op=|cmd=`, ScoreModifier: 0.2, DepthBonus: 1, Description: "动态操作"},
}

// 低价值路径模式
var defaultLowValuePatterns = []*PathPattern{
	// 静态资源
	{Pattern: `\.(css|js|ico|map)$`, ScoreModifier: -0.5, DepthBonus: -2, Description: "静态资源"},
	{Pattern: `/(static|assets|public|dist|build)/`, ScoreModifier: -0.3, DepthBonus: -1, Description: "静态目录"},

	// 媒体文件
	{Pattern: `\.(jpg|jpeg|png|gif|svg|webp|mp4|mp3)`, ScoreModifier: -0.5, DepthBonus: -2, Description: "媒体文件"},

	// 帮助/关于
	{Pattern: `/(help|about|contact|faq|terms|privacy|legal)$`, ScoreModifier: -0.2, DepthBonus: -1, Description: "信息页面"},

	// 新闻/博客列表
	{Pattern: `/(news|blog|article|post)/page/[0-9]+`, ScoreModifier: -0.15, DepthBonus: -1, Description: "分页列表"},

	// 标签/分类页
	{Pattern: `/(tag|category|archive)/`, ScoreModifier: -0.1, DepthBonus: -1, Description: "归档页面"},

	// 第三方
	{Pattern: `(google|facebook|twitter|linkedin)\.com`, ScoreModifier: -0.5, DepthBonus: -3, Description: "外部链接"},
}

// DefaultSmartDepthConfig 默认配置
func DefaultSmartDepthConfig() *SmartDepthConfig {
	baseDepth := conf.GlobalConfig.BrowserConf.MaxDepth
	if baseDepth <= 0 {
		baseDepth = 5
	}

	return &SmartDepthConfig{
		BaseMaxDepth:       baseDepth,
		MinDepth:           1,
		MaxDepth:           baseDepth + 3, // 允许额外3层深度
		HighValueThreshold: 0.6,
		LowValueThreshold:  0.3,
		EnableLearning:     true,
	}
}

// NewSmartDepthController 创建智能深度控制器
func NewSmartDepthController(config *SmartDepthConfig) *SmartDepthController {
	if config == nil {
		config = DefaultSmartDepthConfig()
	}

	sdc := &SmartDepthController{
		baseMaxDepth: config.BaseMaxDepth,
		pageScores:   make(map[string]*PageScore),
		domainStats:  make(map[string]*DomainStats),
		pathPatterns: make(map[string]*PathPattern),
		config:       config,
	}

	// 加载默认模式
	for _, p := range defaultHighValuePatterns {
		if re, err := regexp.Compile("(?i)" + p.Pattern); err == nil {
			p.Regex = re
			sdc.pathPatterns[p.Pattern] = p
		}
	}
	for _, p := range defaultLowValuePatterns {
		if re, err := regexp.Compile("(?i)" + p.Pattern); err == nil {
			p.Regex = re
			sdc.pathPatterns[p.Pattern] = p
		}
	}

	return sdc
}

// CalculateAllowedDepth 计算允许的爬取深度
func (sdc *SmartDepthController) CalculateAllowedDepth(urlInfo *UrlInfo, parentScore float64) int {
	if urlInfo == nil {
		return sdc.baseMaxDepth
	}

	// 计算页面评分
	score := sdc.ScoreURL(urlInfo.Url)

	// 基础深度
	allowedDepth := sdc.baseMaxDepth

	// 根据分数调整深度
	if score.Score >= sdc.config.HighValueThreshold {
		// 高价值页面，增加深度
		bonus := int((score.Score - sdc.config.HighValueThreshold) * 5)
		if bonus > 3 {
			bonus = 3
		}
		allowedDepth += bonus
		atomic.AddInt64(&sdc.highValuePages, 1)
		log.Logger.Debugf("SmartDepth: high value URL %s, score=%.2f, depth=%d", urlInfo.Url, score.Score, allowedDepth)
	} else if score.Score < sdc.config.LowValueThreshold {
		// 低价值页面，减少深度
		penalty := int((sdc.config.LowValueThreshold - score.Score) * 3)
		if penalty > 2 {
			penalty = 2
		}
		allowedDepth -= penalty
		atomic.AddInt64(&sdc.lowValuePages, 1)
	}

	// 父页面分数影响
	if parentScore >= sdc.config.HighValueThreshold {
		allowedDepth += 1
	}

	// 确保在范围内
	if allowedDepth < sdc.config.MinDepth {
		allowedDepth = sdc.config.MinDepth
	}
	if allowedDepth > sdc.config.MaxDepth {
		allowedDepth = sdc.config.MaxDepth
	}

	atomic.AddInt64(&sdc.depthAdjustments, 1)
	score.AdjustedDepth = allowedDepth

	// 缓存分数
	sdc.mu.Lock()
	sdc.pageScores[urlInfo.Url] = score
	sdc.mu.Unlock()

	return allowedDepth
}

// ScoreURL 对URL进行评分
func (sdc *SmartDepthController) ScoreURL(urlStr string) *PageScore {
	score := &PageScore{
		URL:     urlStr,
		Score:   0.5, // 基础分
		Factors: make(map[string]float64),
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return score
	}

	path := parsed.Path
	query := parsed.RawQuery

	// 1. 路径模式匹配
	for _, pattern := range sdc.pathPatterns {
		if pattern.Regex != nil && pattern.Regex.MatchString(urlStr) {
			score.Score += pattern.ScoreModifier
			score.Factors[pattern.Description] = pattern.ScoreModifier
		}
	}

	// 2. 路径深度评估
	pathDepth := len(strings.Split(strings.Trim(path, "/"), "/"))
	if pathDepth <= 2 {
		score.Score += 0.1
		score.Factors["shallow_path"] = 0.1
	} else if pathDepth >= 5 {
		score.Score -= 0.1
		score.Factors["deep_path"] = -0.1
	}

	// 3. 查询参数评估
	if query != "" {
		params, _ := url.ParseQuery(query)
		paramCount := len(params)

		// 有参数通常更有价值
		if paramCount > 0 && paramCount <= 5 {
			score.Score += 0.1
			score.Factors["has_params"] = 0.1
		}

		// 检查敏感参数
		sensitiveParams := []string{"id", "user", "token", "key", "admin", "action", "do", "op"}
		for _, sp := range sensitiveParams {
			if _, ok := params[sp]; ok {
				score.Score += 0.15
				score.Factors["sensitive_param_"+sp] = 0.15
				break
			}
		}
	}

	// 4. 文件扩展名评估
	if strings.HasSuffix(path, ".php") || strings.HasSuffix(path, ".asp") || strings.HasSuffix(path, ".jsp") {
		score.Score += 0.1
		score.Factors["dynamic_script"] = 0.1
	}

	// 5. 特殊关键词
	keywords := map[string]float64{
		"edit":   0.15,
		"delete": 0.15,
		"create": 0.15,
		"new":    0.1,
		"submit": 0.15,
		"update": 0.15,
		"manage": 0.2,
		"config": 0.2,
	}

	pathLower := strings.ToLower(path)
	for kw, bonus := range keywords {
		if strings.Contains(pathLower, kw) {
			score.Score += bonus
			score.Factors["keyword_"+kw] = bonus
		}
	}

	// 6. 域名学习（如果启用）
	if sdc.config.EnableLearning {
		sdc.updateDomainStats(parsed.Host, score.Score)
	}

	// 归一化分数到 0-1
	if score.Score < 0 {
		score.Score = 0
	}
	if score.Score > 1 {
		score.Score = 1
	}

	return score
}

// updateDomainStats 更新域名统计
func (sdc *SmartDepthController) updateDomainStats(domain string, score float64) {
	sdc.mu.Lock()
	defer sdc.mu.Unlock()

	stats, ok := sdc.domainStats[domain]
	if !ok {
		stats = &DomainStats{
			Domain:      domain,
			CommonPaths: make(map[string]int),
		}
		sdc.domainStats[domain] = stats
	}

	stats.TotalPages++
	if score >= sdc.config.HighValueThreshold {
		stats.HighValueCount++
	} else if score < sdc.config.LowValueThreshold {
		stats.LowValueCount++
	}

	// 更新平均分
	stats.AvgScore = (stats.AvgScore*float64(stats.TotalPages-1) + score) / float64(stats.TotalPages)
}

// ShouldCrawl 判断是否应该爬取
func (sdc *SmartDepthController) ShouldCrawl(urlInfo *UrlInfo) bool {
	if urlInfo == nil {
		return false
	}

	// 计算允许深度
	allowedDepth := sdc.CalculateAllowedDepth(urlInfo, 0.5)

	// 检查当前深度
	if urlInfo.Depth > allowedDepth {
		log.Logger.Debugf("SmartDepth: skip URL %s, depth %d > allowed %d", urlInfo.Url, urlInfo.Depth, allowedDepth)
		return false
	}

	return true
}

// GetPageScore 获取页面评分
func (sdc *SmartDepthController) GetPageScore(urlStr string) (*PageScore, bool) {
	sdc.mu.RLock()
	defer sdc.mu.RUnlock()
	score, ok := sdc.pageScores[urlStr]
	return score, ok
}

// AddPattern 添加自定义路径模式
func (sdc *SmartDepthController) AddPattern(pattern string, scoreModifier float64, depthBonus int, description string) error {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return err
	}

	sdc.mu.Lock()
	defer sdc.mu.Unlock()

	sdc.pathPatterns[pattern] = &PathPattern{
		Pattern:       pattern,
		Regex:         re,
		ScoreModifier: scoreModifier,
		DepthBonus:    depthBonus,
		Description:   description,
	}

	return nil
}

// Stats 获取统计信息
func (sdc *SmartDepthController) Stats() SmartDepthStats {
	sdc.mu.RLock()
	defer sdc.mu.RUnlock()

	return SmartDepthStats{
		BaseMaxDepth:     sdc.baseMaxDepth,
		DepthAdjustments: atomic.LoadInt64(&sdc.depthAdjustments),
		HighValuePages:   atomic.LoadInt64(&sdc.highValuePages),
		LowValuePages:    atomic.LoadInt64(&sdc.lowValuePages),
		CachedScores:     len(sdc.pageScores),
		TrackedDomains:   len(sdc.domainStats),
	}
}

// SmartDepthStats 统计信息
type SmartDepthStats struct {
	BaseMaxDepth     int   `json:"base_max_depth"`
	DepthAdjustments int64 `json:"depth_adjustments"`
	HighValuePages   int64 `json:"high_value_pages"`
	LowValuePages    int64 `json:"low_value_pages"`
	CachedScores     int   `json:"cached_scores"`
	TrackedDomains   int   `json:"tracked_domains"`
}

// PriorityCalculator 优先级计算器（用于调度）
type PriorityCalculator struct {
	depthController *SmartDepthController
}

// NewPriorityCalculator 创建优先级计算器
func NewPriorityCalculator(dc *SmartDepthController) *PriorityCalculator {
	return &PriorityCalculator{
		depthController: dc,
	}
}

// CalculatePriority 计算URL的爬取优先级
func (pc *PriorityCalculator) CalculatePriority(urlInfo *UrlInfo) int {
	if pc.depthController == nil {
		return 50 // 默认优先级
	}

	score := pc.depthController.ScoreURL(urlInfo.Url)

	// 基础优先级 50，根据分数调整
	priority := 50 + int(score.Score*50)

	// 深度惩罚
	priority -= urlInfo.Depth * 5

	// 来源类型加成
	switch urlInfo.SourceType {
	case "homePage":
		priority += 30
	case "api":
		priority += 20
	case "form":
		priority += 15
	case "passive_xhr", "passive_fetch":
		priority += 25
	}

	// 确保在有效范围内
	if priority < 1 {
		priority = 1
	}
	if priority > 100 {
		priority = 100
	}

	return priority
}

// URLValueClassifier URL价值分类器
type URLValueClassifier struct {
	patterns []*ClassificationPattern
}

// ClassificationPattern 分类模式
type ClassificationPattern struct {
	Name     string
	Pattern  *regexp.Regexp
	Category string // high_value, medium_value, low_value, skip
	Weight   float64
}

// NewURLValueClassifier 创建分类器
func NewURLValueClassifier() *URLValueClassifier {
	classifier := &URLValueClassifier{
		patterns: make([]*ClassificationPattern, 0),
	}

	// 添加默认模式
	classificationRules := []struct {
		name     string
		pattern  string
		category string
		weight   float64
	}{
		// 高价值
		{"admin_panel", `/(admin|backend|manage|dashboard)`, "high_value", 0.9},
		{"api_endpoint", `/(api|rest|graphql)/`, "high_value", 0.85},
		{"auth_page", `/(login|logout|auth|signin|signup)`, "high_value", 0.8},
		{"user_data", `/(user|profile|account|member)`, "high_value", 0.75},
		{"config", `/(config|setting|option)`, "high_value", 0.8},
		{"upload", `/(upload|file|attachment)`, "high_value", 0.85},

		// 中等价值
		{"search", `/(search|query|find)`, "medium_value", 0.6},
		{"list_page", `/(list|index|browse)`, "medium_value", 0.5},
		{"detail_page", `/(detail|view|show)/`, "medium_value", 0.55},

		// 低价值
		{"static", `/(static|assets|public)/`, "low_value", 0.2},
		{"help", `/(help|faq|about|contact)$`, "low_value", 0.3},
		{"pagination", `/page/[0-9]+`, "low_value", 0.25},

		// 跳过
		{"media", `\.(jpg|png|gif|mp4|mp3|pdf)$`, "skip", 0},
		{"static_assets", `\.(css|js|woff|ico)$`, "skip", 0},
	}

	for _, rule := range classificationRules {
		if re, err := regexp.Compile("(?i)" + rule.pattern); err == nil {
			classifier.patterns = append(classifier.patterns, &ClassificationPattern{
				Name:     rule.name,
				Pattern:  re,
				Category: rule.category,
				Weight:   rule.weight,
			})
		}
	}

	return classifier
}

// Classify 分类URL
func (c *URLValueClassifier) Classify(urlStr string) (category string, weight float64, matchedPatterns []string) {
	category = "medium_value"
	weight = 0.5
	matchedPatterns = make([]string, 0)

	var maxWeight float64 = 0.5
	for _, p := range c.patterns {
		if p.Pattern.MatchString(urlStr) {
			matchedPatterns = append(matchedPatterns, p.Name)
			if p.Weight > maxWeight {
				maxWeight = p.Weight
				category = p.Category
			}
		}
	}

	weight = maxWeight
	return
}
