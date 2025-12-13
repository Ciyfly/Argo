package engine

import (
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"argo/pkg/conf"
	"argo/pkg/log"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// SmartFormFiller 智能表单填充器
type SmartFormFiller struct {
	mu             sync.RWMutex
	fieldPatterns  map[string]*FieldPattern  // 字段模式识别
	filledForms    map[string]bool           // 已填充表单缓存
	customValues   map[string]string         // 用户自定义值
	learnedFields  map[string]string         // 学习到的字段映射
	contextAware   bool                      // 是否启用上下文感知
}

// FieldPattern 字段识别模式
type FieldPattern struct {
	NamePatterns       []string // name 属性匹配模式
	PlaceholderPatterns []string // placeholder 匹配模式
	LabelPatterns      []string // label 文本匹配模式
	TypePatterns       []string // input type 匹配
	ValueGenerator     func() string // 值生成器
	Priority           int      // 优先级
}

// FormContext 表单上下文信息
type FormContext struct {
	PageURL      string
	PageTitle    string
	FormAction   string
	FormMethod   string
	NearbyText   string // 表单附近的文本
	IsLoginForm  bool
	IsSearchForm bool
	IsRegisterForm bool
	IsContactForm bool
}

// 默认字段模式
var defaultFieldPatterns = map[string]*FieldPattern{
	"username": {
		NamePatterns:       []string{"user", "username", "login", "account", "uid", "uname", "loginname"},
		PlaceholderPatterns: []string{"用户名", "账号", "username", "account"},
		LabelPatterns:      []string{"用户名", "账号", "登录名", "Username", "Account"},
		TypePatterns:       []string{"text"},
		ValueGenerator:     func() string { return "testuser" + randomSuffix(4) },
		Priority:           100,
	},
	"email": {
		NamePatterns:       []string{"email", "mail", "e-mail", "useremail"},
		PlaceholderPatterns: []string{"邮箱", "email", "电子邮件"},
		LabelPatterns:      []string{"邮箱", "Email", "电子邮件"},
		TypePatterns:       []string{"email", "text"},
		ValueGenerator:     func() string { return "test" + randomSuffix(6) + "@example.com" },
		Priority:           90,
	},
	"password": {
		NamePatterns:       []string{"password", "passwd", "pass", "pwd", "userpwd"},
		PlaceholderPatterns: []string{"密码", "password"},
		LabelPatterns:      []string{"密码", "Password"},
		TypePatterns:       []string{"password"},
		ValueGenerator:     func() string { return "Test@" + randomSuffix(6) + "!" },
		Priority:           100,
	},
	"phone": {
		NamePatterns:       []string{"phone", "mobile", "tel", "telephone", "cellphone", "手机"},
		PlaceholderPatterns: []string{"手机", "电话", "phone", "mobile"},
		LabelPatterns:      []string{"手机号", "电话", "Phone", "Mobile"},
		TypePatterns:       []string{"tel", "text", "number"},
		ValueGenerator:     func() string { return "138" + randomDigits(8) },
		Priority:           85,
	},
	"search": {
		NamePatterns:       []string{"q", "s", "query", "search", "keyword", "keywords", "wd", "word"},
		PlaceholderPatterns: []string{"搜索", "search", "查找", "关键词"},
		LabelPatterns:      []string{"搜索", "Search"},
		TypePatterns:       []string{"search", "text"},
		ValueGenerator:     func() string { return "test" },
		Priority:           95,
	},
	"name": {
		NamePatterns:       []string{"name", "fullname", "realname", "truename", "姓名"},
		PlaceholderPatterns: []string{"姓名", "名字", "name"},
		LabelPatterns:      []string{"姓名", "Name", "真实姓名"},
		TypePatterns:       []string{"text"},
		ValueGenerator:     func() string { return "Test User" },
		Priority:           80,
	},
	"address": {
		NamePatterns:       []string{"address", "addr", "street", "地址"},
		PlaceholderPatterns: []string{"地址", "address"},
		LabelPatterns:      []string{"地址", "Address"},
		TypePatterns:       []string{"text"},
		ValueGenerator:     func() string { return "123 Test Street" },
		Priority:           70,
	},
	"city": {
		NamePatterns:       []string{"city", "城市"},
		PlaceholderPatterns: []string{"城市", "city"},
		LabelPatterns:      []string{"城市", "City"},
		TypePatterns:       []string{"text"},
		ValueGenerator:     func() string { return "Beijing" },
		Priority:           65,
	},
	"company": {
		NamePatterns:       []string{"company", "org", "organization", "公司"},
		PlaceholderPatterns: []string{"公司", "company", "organization"},
		LabelPatterns:      []string{"公司", "Company", "组织"},
		TypePatterns:       []string{"text"},
		ValueGenerator:     func() string { return "Test Company" },
		Priority:           60,
	},
	"comment": {
		NamePatterns:       []string{"comment", "message", "content", "body", "text", "desc", "description"},
		PlaceholderPatterns: []string{"留言", "评论", "内容", "comment", "message"},
		LabelPatterns:      []string{"留言", "评论", "内容", "Comment", "Message"},
		TypePatterns:       []string{"textarea", "text"},
		ValueGenerator:     func() string { return "This is a test comment." },
		Priority:           50,
	},
	"url": {
		NamePatterns:       []string{"url", "website", "site", "homepage"},
		PlaceholderPatterns: []string{"网址", "url", "website"},
		LabelPatterns:      []string{"网址", "URL", "Website"},
		TypePatterns:       []string{"url", "text"},
		ValueGenerator:     func() string { return "https://example.com" },
		Priority:           55,
	},
	"date": {
		NamePatterns:       []string{"date", "birthday", "dob", "日期"},
		PlaceholderPatterns: []string{"日期", "date"},
		LabelPatterns:      []string{"日期", "Date", "生日"},
		TypePatterns:       []string{"date", "text"},
		ValueGenerator:     func() string { return "2024-01-15" },
		Priority:           45,
	},
	"number": {
		NamePatterns:       []string{"amount", "price", "quantity", "num", "count"},
		PlaceholderPatterns: []string{"数量", "金额", "number"},
		LabelPatterns:      []string{"数量", "Number", "Amount"},
		TypePatterns:       []string{"number", "text"},
		ValueGenerator:     func() string { return "1" },
		Priority:           40,
	},
	"captcha": {
		NamePatterns:       []string{"captcha", "code", "verifycode", "verify", "验证码", "checkcode"},
		PlaceholderPatterns: []string{"验证码", "captcha", "code"},
		LabelPatterns:      []string{"验证码", "Captcha", "Code"},
		TypePatterns:       []string{"text"},
		ValueGenerator:     func() string { return "" }, // 验证码不填
		Priority:           1, // 最低优先级
	},
}

// NewSmartFormFiller 创建智能表单填充器
func NewSmartFormFiller() *SmartFormFiller {
	sff := &SmartFormFiller{
		fieldPatterns: make(map[string]*FieldPattern),
		filledForms:   make(map[string]bool),
		customValues:  make(map[string]string),
		learnedFields: make(map[string]string),
		contextAware:  true,
	}

	// 加载默认模式
	for name, pattern := range defaultFieldPatterns {
		sff.fieldPatterns[name] = pattern
	}

	// 加载配置中的自定义值
	if conf.GlobalConfig.LoginConf.Username != "" {
		sff.customValues["username"] = conf.GlobalConfig.LoginConf.Username
	}
	if conf.GlobalConfig.LoginConf.Password != "" {
		sff.customValues["password"] = conf.GlobalConfig.LoginConf.Password
	}
	if conf.GlobalConfig.LoginConf.Email != "" {
		sff.customValues["email"] = conf.GlobalConfig.LoginConf.Email
	}
	if conf.GlobalConfig.LoginConf.Phone != "" {
		sff.customValues["phone"] = conf.GlobalConfig.LoginConf.Phone
	}

	return sff
}

// AnalyzeFormContext 分析表单上下文
func (sff *SmartFormFiller) AnalyzeFormContext(page *rod.Page, formElement *rod.Element) (*FormContext, error) {
	ctx := &FormContext{}

	// 获取页面信息
	info, err := page.Info()
	if err == nil {
		ctx.PageURL = info.URL
		ctx.PageTitle = info.Title
	}

	// 获取表单属性
	action, _ := formElement.Attribute("action")
	if action != nil {
		ctx.FormAction = *action
	}
	method, _ := formElement.Attribute("method")
	if method != nil {
		ctx.FormMethod = strings.ToUpper(*method)
	}

	// 分析表单类型
	formHTML, _ := formElement.HTML()
	formHTMLLower := strings.ToLower(formHTML)
	titleLower := strings.ToLower(ctx.PageTitle)

	// 登录表单检测
	loginKeywords := []string{"login", "signin", "登录", "登陆", "sign in"}
	for _, kw := range loginKeywords {
		if strings.Contains(formHTMLLower, kw) || strings.Contains(titleLower, kw) {
			ctx.IsLoginForm = true
			break
		}
	}

	// 搜索表单检测
	searchKeywords := []string{"search", "搜索", "查找", "检索"}
	for _, kw := range searchKeywords {
		if strings.Contains(formHTMLLower, kw) {
			ctx.IsSearchForm = true
			break
		}
	}

	// 注册表单检测
	registerKeywords := []string{"register", "signup", "注册", "sign up", "create account"}
	for _, kw := range registerKeywords {
		if strings.Contains(formHTMLLower, kw) || strings.Contains(titleLower, kw) {
			ctx.IsRegisterForm = true
			break
		}
	}

	// 联系表单检测
	contactKeywords := []string{"contact", "联系", "留言", "message", "feedback"}
	for _, kw := range contactKeywords {
		if strings.Contains(formHTMLLower, kw) || strings.Contains(titleLower, kw) {
			ctx.IsContactForm = true
			break
		}
	}

	return ctx, nil
}

// FillForm 智能填充表单
func (sff *SmartFormFiller) FillForm(page *rod.Page, formSelector string) error {
	if page == nil {
		return nil
	}

	// 查找表单
	var formElement *rod.Element
	var err error

	if formSelector != "" {
		formElement, err = page.Element(formSelector)
	} else {
		formElement, err = page.Element("form")
	}

	if err != nil {
		log.Logger.Debugf("SmartFormFiller: form not found: %v", err)
		return nil
	}

	// 分析表单上下文
	ctx, _ := sff.AnalyzeFormContext(page, formElement)

	// 获取所有输入字段
	inputs, err := formElement.Elements("input, textarea, select")
	if err != nil {
		return err
	}

	for _, input := range inputs {
		if err := sff.fillField(page, input, ctx); err != nil {
			log.Logger.Debugf("SmartFormFiller: fill field error: %v", err)
		}
		// 添加人类化延迟
		time.Sleep(time.Duration(50+rand.Intn(150)) * time.Millisecond)
	}

	return nil
}

// fillField 填充单个字段
func (sff *SmartFormFiller) fillField(page *rod.Page, element *rod.Element, ctx *FormContext) error {
	// 获取字段属性
	tagName, _ := element.Property("tagName")
	name, _ := element.Attribute("name")
	inputType, _ := element.Attribute("type")
	placeholder, _ := element.Attribute("placeholder")

	if name == nil || *name == "" {
		id, _ := element.Attribute("id")
		if id != nil {
			name = id
		}
	}

	tag := ""
	if tagName.Str() != "" {
		tag = strings.ToLower(tagName.Str())
	}

	fieldType := "text"
	if inputType != nil && *inputType != "" {
		fieldType = strings.ToLower(*inputType)
	}

	// 跳过特定类型
	skipTypes := map[string]bool{
		"hidden": true, "submit": true, "button": true,
		"image": true, "reset": true, "file": true,
	}
	if skipTypes[fieldType] {
		return nil
	}

	// 获取当前值
	currentValue, _ := element.Property("value")
	if currentValue.Str() != "" && fieldType != "password" {
		return nil // 已有值，跳过
	}

	// 识别字段类型
	fieldName := ""
	if name != nil {
		fieldName = *name
	}
	placeholderStr := ""
	if placeholder != nil {
		placeholderStr = *placeholder
	}

	// 获取关联的 label
	labelText := sff.getAssociatedLabel(page, element, fieldName)

	// 匹配字段模式
	value := sff.matchFieldValue(fieldName, placeholderStr, labelText, fieldType, ctx)

	if value == "" {
		return nil
	}

	// 填充值
	return sff.inputValue(page, element, value, tag, fieldType)
}

// getAssociatedLabel 获取关联的 label 文本
func (sff *SmartFormFiller) getAssociatedLabel(page *rod.Page, element *rod.Element, fieldName string) string {
	if fieldName == "" {
		return ""
	}

	// 尝试通过 for 属性查找 label
	labelSelector := "label[for='" + fieldName + "']"
	label, err := page.Element(labelSelector)
	if err == nil && label != nil {
		text, _ := label.Text()
		return text
	}

	// 尝试查找父级 label
	parent, err := element.Parent()
	if err == nil && parent != nil {
		tagName, _ := parent.Property("tagName")
		if tagName.Str() == "LABEL" {
			text, _ := parent.Text()
			return text
		}
	}

	return ""
}

// matchFieldValue 匹配字段值
func (sff *SmartFormFiller) matchFieldValue(fieldName, placeholder, labelText, fieldType string, ctx *FormContext) string {
	sff.mu.RLock()
	defer sff.mu.RUnlock()

	// 首先检查自定义值
	for key, val := range sff.customValues {
		if sff.fieldMatches(fieldName, placeholder, labelText, sff.fieldPatterns[key]) {
			return val
		}
	}

	// 检查学习到的字段
	if val, ok := sff.learnedFields[fieldName]; ok {
		return val
	}

	// 匹配默认模式
	var bestMatch *FieldPattern
	var bestPriority int

	for _, pattern := range sff.fieldPatterns {
		if sff.fieldMatches(fieldName, placeholder, labelText, pattern) {
			if pattern.Priority > bestPriority {
				bestMatch = pattern
				bestPriority = pattern.Priority
			}
		}
	}

	if bestMatch != nil && bestMatch.ValueGenerator != nil {
		return bestMatch.ValueGenerator()
	}

	// 根据输入类型提供默认值
	switch fieldType {
	case "email":
		return "test" + randomSuffix(4) + "@example.com"
	case "password":
		return "Test@123456!"
	case "tel":
		return "13800138000"
	case "url":
		return "https://example.com"
	case "number":
		return "1"
	case "date":
		return "2024-01-01"
	case "checkbox", "radio":
		return "" // 不自动勾选
	default:
		// 上下文感知
		if ctx != nil {
			if ctx.IsSearchForm {
				return "test"
			}
		}
		return "test"
	}
}

// fieldMatches 检查字段是否匹配模式
func (sff *SmartFormFiller) fieldMatches(fieldName, placeholder, labelText string, pattern *FieldPattern) bool {
	if pattern == nil {
		return false
	}

	fieldNameLower := strings.ToLower(fieldName)
	placeholderLower := strings.ToLower(placeholder)
	labelLower := strings.ToLower(labelText)

	// 检查 name 模式
	for _, p := range pattern.NamePatterns {
		if strings.Contains(fieldNameLower, strings.ToLower(p)) {
			return true
		}
	}

	// 检查 placeholder 模式
	for _, p := range pattern.PlaceholderPatterns {
		if strings.Contains(placeholderLower, strings.ToLower(p)) {
			return true
		}
	}

	// 检查 label 模式
	for _, p := range pattern.LabelPatterns {
		if strings.Contains(labelLower, strings.ToLower(p)) {
			return true
		}
	}

	return false
}

// inputValue 输入值到字段
func (sff *SmartFormFiller) inputValue(page *rod.Page, element *rod.Element, value, tagName, fieldType string) error {
	// 先聚焦
	err := element.Focus()
	if err != nil {
		return err
	}

	// 清空现有内容
	element.SelectAllText()

	// 模拟人类输入延迟
	time.Sleep(time.Duration(30+rand.Intn(70)) * time.Millisecond)

	switch tagName {
	case "select":
		// 选择框：选择第一个非空选项
		options, err := element.Elements("option")
		if err == nil && len(options) > 1 {
			// 跳过第一个（通常是占位符）
			idx := 1
			if len(options) > 2 {
				idx = 1 + rand.Intn(len(options)-1)
			}
			options[idx].Click(proto.InputMouseButtonLeft, 1)
		}
	case "textarea":
		element.Input(value)
	default:
		switch fieldType {
		case "checkbox":
			checked, _ := element.Property("checked")
			if !checked.Bool() {
				element.Click(proto.InputMouseButtonLeft, 1)
			}
		case "radio":
			element.Click(proto.InputMouseButtonLeft, 1)
		default:
			element.Input(value)
		}
	}

	return nil
}

// SetCustomValue 设置自定义字段值
func (sff *SmartFormFiller) SetCustomValue(fieldType, value string) {
	sff.mu.Lock()
	defer sff.mu.Unlock()
	sff.customValues[fieldType] = value
}

// AddFieldPattern 添加字段识别模式
func (sff *SmartFormFiller) AddFieldPattern(name string, pattern *FieldPattern) {
	sff.mu.Lock()
	defer sff.mu.Unlock()
	sff.fieldPatterns[name] = pattern
}

// LearnField 学习新的字段映射
func (sff *SmartFormFiller) LearnField(fieldName, value string) {
	sff.mu.Lock()
	defer sff.mu.Unlock()
	sff.learnedFields[fieldName] = value
}

// FillAllForms 填充页面上的所有表单
func (sff *SmartFormFiller) FillAllForms(page *rod.Page) error {
	if page == nil {
		return nil
	}

	forms, err := page.Elements("form")
	if err != nil {
		return nil
	}

	for i, form := range forms {
		// 生成表单标识
		action, _ := form.Attribute("action")
		formID := ""
		if action != nil {
			formID = *action
		} else {
			formID = page.MustInfo().URL + "_form_" + string(rune(i))
		}

		// 检查是否已填充
		sff.mu.RLock()
		filled := sff.filledForms[formID]
		sff.mu.RUnlock()

		if filled {
			continue
		}

		// 分析并填充
		ctx, _ := sff.AnalyzeFormContext(page, form)

		inputs, err := form.Elements("input, textarea, select")
		if err != nil {
			continue
		}

		for _, input := range inputs {
			sff.fillField(page, input, ctx)
			time.Sleep(time.Duration(30+rand.Intn(100)) * time.Millisecond)
		}

		// 标记为已填充
		sff.mu.Lock()
		sff.filledForms[formID] = true
		sff.mu.Unlock()

		log.Logger.Debugf("SmartFormFiller: filled form %s", formID)
	}

	return nil
}

// DetectAndFillSearchForm 检测并填充搜索表单
func (sff *SmartFormFiller) DetectAndFillSearchForm(page *rod.Page) error {
	// 常见搜索框选择器
	searchSelectors := []string{
		"input[type='search']",
		"input[name='q']",
		"input[name='s']",
		"input[name='query']",
		"input[name='search']",
		"input[name='keyword']",
		"input[name='wd']",
		"input[placeholder*='搜索']",
		"input[placeholder*='search']",
		"input.search",
		"#search",
		"#q",
	}

	for _, selector := range searchSelectors {
		element, err := page.Element(selector)
		if err != nil {
			continue
		}

		// 找到搜索框，填充
		element.Focus()
		time.Sleep(50 * time.Millisecond)
		element.Input("test")
		log.Logger.Debugf("SmartFormFiller: filled search form with selector: %s", selector)
		return nil
	}

	return nil
}

// 辅助函数
func randomSuffix(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func randomDigits(length int) string {
	const digits = "0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = digits[rand.Intn(len(digits))]
	}
	return string(b)
}

// ExtractFormData 提取表单数据用于分析
func (sff *SmartFormFiller) ExtractFormData(page *rod.Page) []map[string]interface{} {
	var result []map[string]interface{}

	forms, err := page.Elements("form")
	if err != nil {
		return result
	}

	for _, form := range forms {
		formData := make(map[string]interface{})

		action, _ := form.Attribute("action")
		method, _ := form.Attribute("method")

		if action != nil {
			formData["action"] = *action
		}
		if method != nil {
			formData["method"] = *method
		}

		var fields []map[string]string
		inputs, _ := form.Elements("input, textarea, select")
		for _, input := range inputs {
			field := make(map[string]string)
			name, _ := input.Attribute("name")
			inputType, _ := input.Attribute("type")
			placeholder, _ := input.Attribute("placeholder")

			if name != nil {
				field["name"] = *name
			}
			if inputType != nil {
				field["type"] = *inputType
			}
			if placeholder != nil {
				field["placeholder"] = *placeholder
			}

			if len(field) > 0 {
				fields = append(fields, field)
			}
		}

		formData["fields"] = fields
		result = append(result, formData)
	}

	return result
}

// IdentifyFormType 识别表单类型
func IdentifyFormType(formHTML string) string {
	formLower := strings.ToLower(formHTML)

	patterns := map[string]*regexp.Regexp{
		"login":    regexp.MustCompile(`(login|signin|登录|sign.?in)`),
		"register": regexp.MustCompile(`(register|signup|注册|sign.?up|create.?account)`),
		"search":   regexp.MustCompile(`(search|搜索|查找|检索)`),
		"contact":  regexp.MustCompile(`(contact|联系|留言|message|feedback)`),
		"payment":  regexp.MustCompile(`(payment|checkout|付款|支付|结算)`),
		"subscribe": regexp.MustCompile(`(subscribe|newsletter|订阅)`),
	}

	for formType, pattern := range patterns {
		if pattern.MatchString(formLower) {
			return formType
		}
	}

	return "unknown"
}
