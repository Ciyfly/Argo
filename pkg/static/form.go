package static

import (
	"argo/pkg/log"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"
)

// FormInfo 表单信息
type FormInfo struct {
	Action  string       // 表单提交地址
	Method  string       // 提交方法 GET/POST
	EncType string       // 编码类型
	Fields  []FormField  // 表单字段
	BaseURL string       // 基准 URL
}

// FormField 表单字段
type FormField struct {
	Name        string // 字段名
	Type        string // 字段类型
	Value       string // 默认值
	Placeholder string // 占位符
	Required    bool   // 是否必填
	TagName     string // 标签名 (input/select/textarea)
}

// 表单字段填充建议
var formFillSuggestions = map[string]string{
	// 用户相关
	"username":     "testuser",
	"user":         "testuser",
	"login":        "testuser",
	"account":      "testuser",
	"name":         "Test User",
	"fullname":     "Test User",
	"nickname":     "tester",

	// 邮箱相关
	"email":        "test@example.com",
	"mail":         "test@example.com",
	"e-mail":       "test@example.com",

	// 密码相关
	"password":     "Test123!@#",
	"passwd":       "Test123!@#",
	"pass":         "Test123!@#",
	"pwd":          "Test123!@#",
	"confirm":      "Test123!@#",
	"repassword":   "Test123!@#",

	// 手机相关
	"phone":        "13800138000",
	"mobile":       "13800138000",
	"tel":          "13800138000",
	"telephone":    "13800138000",
	"cellphone":    "13800138000",

	// 搜索相关
	"search":       "test",
	"q":            "test",
	"query":        "test",
	"keyword":      "test",
	"keywords":     "test",
	"s":            "test",
	"wd":           "test",

	// 地址相关
	"address":      "123 Test Street",
	"city":         "Test City",
	"state":        "Test State",
	"country":      "China",
	"zip":          "100000",
	"zipcode":      "100000",
	"postcode":     "100000",

	// 公司相关
	"company":      "Test Company",
	"organization": "Test Org",

	// 网站相关
	"url":          "https://example.com",
	"website":      "https://example.com",
	"site":         "https://example.com",

	// 描述相关
	"description":  "This is a test description",
	"desc":         "This is a test description",
	"comment":      "Test comment",
	"message":      "Test message",
	"content":      "Test content",
	"body":         "Test body content",
	"text":         "Test text",

	// 数字相关
	"age":          "25",
	"amount":       "100",
	"price":        "99.99",
	"quantity":     "1",
	"number":       "12345",
	"id":           "1",

	// 日期相关
	"date":         "2024-01-01",
	"birthday":     "1990-01-01",
	"dob":          "1990-01-01",

	// 验证码 (不填)
	"captcha":      "",
	"code":         "",
	"verifycode":   "",
}

// ParseForms 从 HTML 中解析所有表单
func ParseForms(htmlStr, baseURL string) []FormInfo {
	forms := []FormInfo{}
	tkn := xhtml.NewTokenizer(strings.NewReader(htmlStr))

	var currentForm *FormInfo
	var inForm bool

	for {
		tt := tkn.Next()
		switch tt {
		case xhtml.ErrorToken:
			return forms

		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			t := tkn.Token()
			tag := strings.ToLower(t.Data)

			if tag == "form" {
				// 开始新表单
				currentForm = &FormInfo{
					Action:  getAttr(t.Attr, "action"),
					Method:  strings.ToUpper(getAttr(t.Attr, "method")),
					EncType: getAttr(t.Attr, "enctype"),
					BaseURL: baseURL,
					Fields:  []FormField{},
				}
				if currentForm.Method == "" {
					currentForm.Method = "GET"
				}
				if currentForm.EncType == "" {
					currentForm.EncType = "application/x-www-form-urlencoded"
				}
				inForm = true
			}

			if inForm && currentForm != nil {
				switch tag {
				case "input":
					field := FormField{
						TagName:     "input",
						Name:        getAttr(t.Attr, "name"),
						Type:        strings.ToLower(getAttr(t.Attr, "type")),
						Value:       getAttr(t.Attr, "value"),
						Placeholder: getAttr(t.Attr, "placeholder"),
						Required:    hasAttr(t.Attr, "required"),
					}
					if field.Type == "" {
						field.Type = "text"
					}
					// 跳过隐藏字段和提交按钮 (保留hidden以便提交)
					if field.Name != "" && field.Type != "submit" && field.Type != "button" && field.Type != "image" {
						currentForm.Fields = append(currentForm.Fields, field)
					}

				case "select":
					field := FormField{
						TagName:  "select",
						Name:     getAttr(t.Attr, "name"),
						Type:     "select",
						Required: hasAttr(t.Attr, "required"),
					}
					if field.Name != "" {
						currentForm.Fields = append(currentForm.Fields, field)
					}

				case "textarea":
					field := FormField{
						TagName:     "textarea",
						Name:        getAttr(t.Attr, "name"),
						Type:        "textarea",
						Placeholder: getAttr(t.Attr, "placeholder"),
						Required:    hasAttr(t.Attr, "required"),
					}
					if field.Name != "" {
						currentForm.Fields = append(currentForm.Fields, field)
					}
				}
			}

		case xhtml.EndTagToken:
			t := tkn.Token()
			if strings.ToLower(t.Data) == "form" && inForm && currentForm != nil {
				// 结束表单，添加到列表
				if len(currentForm.Fields) > 0 || currentForm.Action != "" {
					forms = append(forms, *currentForm)
				}
				currentForm = nil
				inForm = false
			}
		}
	}
}

// getAttr 获取属性值
func getAttr(attrs []xhtml.Attribute, key string) string {
	key = strings.ToLower(key)
	for _, a := range attrs {
		if strings.ToLower(a.Key) == key {
			return a.Val
		}
	}
	return ""
}

// hasAttr 检查属性是否存在
func hasAttr(attrs []xhtml.Attribute, key string) bool {
	key = strings.ToLower(key)
	for _, a := range attrs {
		if strings.ToLower(a.Key) == key {
			return true
		}
	}
	return false
}

// GetFieldValue 根据字段名获取建议填充值
func GetFieldValue(field FormField) string {
	// 如果已有值，直接返回
	if field.Value != "" && field.Type != "password" {
		return field.Value
	}

	nameLower := strings.ToLower(field.Name)

	// 先精确匹配
	if val, ok := formFillSuggestions[nameLower]; ok {
		return val
	}

	// 模糊匹配
	for key, val := range formFillSuggestions {
		if strings.Contains(nameLower, key) {
			return val
		}
	}

	// 根据类型填充默认值
	switch field.Type {
	case "email":
		return "test@example.com"
	case "password":
		return "Test123!@#"
	case "tel":
		return "13800138000"
	case "number":
		return "1"
	case "url":
		return "https://example.com"
	case "date":
		return "2024-01-01"
	case "checkbox", "radio":
		return "on"
	case "hidden":
		return field.Value // 保留原值
	default:
		return "test"
	}
}

// BuildFormURLs 根据表单生成可能的 URL
func BuildFormURLs(form FormInfo) []string {
	urls := []string{}

	// 解析 action URL
	actionURL := form.Action
	if actionURL == "" {
		actionURL = form.BaseURL
	} else {
		// 处理相对路径
		if resolved := HandlerUrl(actionURL, form.BaseURL); resolved != "" {
			actionURL = resolved
		}
	}

	if actionURL == "" {
		return urls
	}

	// 对于 GET 表单，构建带参数的 URL
	if form.Method == "GET" {
		params := url.Values{}
		for _, field := range form.Fields {
			if field.Name != "" {
				params.Set(field.Name, GetFieldValue(field))
			}
		}

		parsedURL, err := url.Parse(actionURL)
		if err != nil {
			return urls
		}

		// 合并现有参数
		existingParams := parsedURL.Query()
		for k, v := range params {
			existingParams[k] = v
		}
		parsedURL.RawQuery = existingParams.Encode()

		urls = append(urls, parsedURL.String())
	} else {
		// 对于 POST 表单，只添加 action URL
		urls = append(urls, actionURL)
	}

	log.Logger.Debugf("form urls: %v", urls)
	return urls
}

// ExtractFormURLs 从 HTML 中提取所有表单相关的 URL
func ExtractFormURLs(htmlStr, baseURL string) []string {
	forms := ParseForms(htmlStr, baseURL)
	var urls []string

	for _, form := range forms {
		formURLs := BuildFormURLs(form)
		urls = append(urls, formURLs...)
	}

	return urls
}
