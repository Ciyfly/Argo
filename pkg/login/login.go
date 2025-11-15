package login

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/utils"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type LoginAutoData struct {
	Username string
	Password string
	Email    string
	Phone    string
	Page     *rod.Page
}

var GlobalLoginAutoData *LoginAutoData

func InitLoginAuto() {
	GlobalLoginAutoData = &LoginAutoData{
		Username: conf.GlobalConfig.LoginConf.Username,
		Password: conf.GlobalConfig.LoginConf.Password,
		Email:    conf.GlobalConfig.LoginConf.Email,
		Phone:    conf.GlobalConfig.LoginConf.Phone,
	}
}

var (
	loginUrlKeywords  = []string{"login", "signin", "passport", "auth"}
	loginTextKeywords = []string{"登录", "登入", "账号登录", "帐号登录", "sign in", "log in"}
	captchaKeywords   = []string{"captcha", "验证码", "人机验证", "滑块验证", "tcaptcha", "geetest", "极验", "verifycode"}
	sliderKeywords    = []string{"slider", "拖动", "滑动", "slide"}
)

var captchaPromptMu sync.Mutex

func parse(page *rod.Page) bool {
	if page == nil {
		return false
	}
	html, _ := page.HTML()
	info, err := utils.GetPageInfoByPage(page)
	if err != nil {
		return false
	}
	url := strings.ToLower(info.URL)
	title := strings.ToLower(info.Title)
	for _, kw := range loginUrlKeywords {
		if strings.Contains(url, kw) {
			return true
		}
	}
	if strings.Contains(title, "登录") || strings.Contains(title, "login") {
		return true
	}
	if hasLoginFormElements(page) {
		return true
	}
	// 判断页面中是否有关键字 针对web1.0页面
	lowHtml := strings.ToLower(html)
	if strings.Contains(lowHtml, "用户登录") || strings.Contains(lowHtml, "忘记密码") || strings.Contains(lowHtml, "登录") && strings.Contains(lowHtml, "密码") {
		return true
	}
	// 针对 web2.0 vue等 需要解析dom 来判断

	return false
}

func (lp *LoginAutoData) Handler(page *rod.Page, stageRecorder func(string)) {
	// 判断这个页面是否需要登录
	if parse(page) {
		// 需要登录 自动化匹配输入框和提交框
		currentUrl, err := utils.GetCurrentUrlByPage(page)
		if err != nil {
			return
		}
		log.Logger.Debugf("try login %s", currentUrl)
		recordStage(stageRecorder, "interaction:login:detect")
		if detected := lp.detectCaptchaAndLog(page, currentUrl, stageRecorder); detected {
			return
		}
		// 自动匹配输入框和密码框测试登录
		lp.Page = page
		lp.tryLogin(stageRecorder)
	} else {
		currentUrl, err := utils.GetCurrentUrlByPage(page)
		if err != nil {
			return
		}
		log.Logger.Debugf("It did not recognize that login was required %s", currentUrl)
	}
}

func hasLoginFormElements(page *rod.Page) bool {
	if page == nil {
		return false
	}
	passwordInputs, err := page.Elements("input[type='password']")
	if err == nil && len(passwordInputs) > 0 {
		return true
	}
	buttons, err := page.Elements("button")
	if err == nil {
		for _, btn := range buttons {
			text, _ := btn.Text()
			if text == "" {
				continue
			}
			low := strings.ToLower(text)
			for _, kw := range loginTextKeywords {
				if strings.Contains(low, strings.ToLower(kw)) {
					return true
				}
			}
		}
	}
	forms, err := page.Elements("form")
	if err == nil {
		for _, form := range forms {
			action, _ := form.Attribute("action")
			if action != nil {
				low := strings.ToLower(*action)
				for _, kw := range loginUrlKeywords {
					if strings.Contains(low, kw) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (lp *LoginAutoData) detectCaptchaAndLog(page *rod.Page, currentUrl string, stageRecorder func(string)) bool {
	if page == nil {
		return false
	}
	recordStage(stageRecorder, "interaction:login:captcha_scan")
	var target *rod.Element
	var slider bool
	selectors := []string{
		"iframe[src*='captcha']",
		"iframe[src*='geetest']",
		"img[src*='captcha']",
		"div[id*='captcha']",
		"div[class*='captcha']",
		"input[name*='captcha']",
	}
	for _, sel := range selectors {
		elems, err := page.Elements(sel)
		if err == nil && len(elems) > 0 {
			target = elems[0]
			break
		}
	}
	if target == nil {
		sliderSelectors := []string{
			"iframe[src*='slider']",
			"div[class*='slider']",
			"div[id*='slider']",
			"canvas[class*='slider']",
		}
		for _, sel := range sliderSelectors {
			elems, err := page.Elements(sel)
			if err == nil && len(elems) > 0 {
				target = elems[0]
				slider = true
				break
			}
		}
	}
	html, _ := page.HTML()
	lowerHTML := strings.ToLower(html)
	matchedKeyword := ""
	for _, kw := range captchaKeywords {
		if strings.Contains(lowerHTML, strings.ToLower(kw)) {
			matchedKeyword = kw
			break
		}
	}
	if target == nil && matchedKeyword == "" {
		return false
	}

	recordStage(stageRecorder, "interaction:login:captcha_detected")
	previewPath, previewBase64, data, err := captureCaptchaPreview(page, target)
	if err != nil {
		log.Logger.Warnf("[captcha] detected captcha on %s but failed to capture preview: %s", currentUrl, err)
	} else {
		log.Logger.Warnf("[captcha] detected captcha on %s. Screenshot saved to %s", currentUrl, previewPath)
		log.Logger.Infof("[captcha] base64 preview (trimmed): %s", previewBase64)
		log.Logger.Infof("[captcha] run `xdg-open %s` 或者使用任意图片查看器打开该文件进行人工识别。", previewPath)
	}
	if matchedKeyword != "" {
		log.Logger.Warnf("[captcha] 页面包含验证码关键字: %s", matchedKeyword)
	}
	if slider || containsSliderKeyword(lowerHTML) {
		recordStage(stageRecorder, "interaction:login:captcha_slider")
		log.Logger.Warn("[captcha] 检测到滑块/拖动式验证码，请后续接入 JS 插件处理（TODO）。当前跳过自动登录。")
		return true
	}
	if len(data) == 0 {
		log.Logger.Warn("[captcha] 无法获取验证码图片，跳过自动登录。")
		return true
	}
	printCaptchaASCII(data)
	code, ok := promptCaptchaInput()
	if !ok || code == "" {
		log.Logger.Warn("[captcha] 未输入验证码，跳过自动登录。")
		return true
	}
	if lp.fillCaptchaInput(page, code) {
		recordStage(stageRecorder, "interaction:login:captcha_manual_filled")
		log.Logger.Infof("[captcha] 已写入人工输入验证码，继续自动登录。")
		return false
	}
	log.Logger.Warn("[captcha] 未找到验证码输入框，无法写入人工输入，跳过自动登录。")
	return true
}

func captureCaptchaPreview(page *rod.Page, target *rod.Element) (string, string, []byte, error) {
	var (
		data []byte
		err  error
	)
	if target != nil {
		data, err = target.Screenshot(proto.PageCaptureScreenshotFormatPng, 90)
	}
	if err != nil || data == nil {
		data, err = page.Screenshot(false, nil)
		if err != nil {
			return "", "", nil, err
		}
	}
	if len(data) == 0 {
		return "", "", nil, fmt.Errorf("captcha screenshot empty")
	}
	dir := filepath.Join("logs", "captcha")
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return "", "", nil, mkErr
	}
	fileName := fmt.Sprintf("captcha_%d.png", time.Now().UnixNano()/1e6)
	path := filepath.Join(dir, fileName)
	if writeErr := os.WriteFile(path, data, 0o644); writeErr != nil {
		return "", "", nil, writeErr
	}
	base64Str := base64.StdEncoding.EncodeToString(data)
	if len(base64Str) > 200 {
		base64Str = base64Str[:200] + "..."
	}
	return path, base64Str, data, nil
}

func recordStage(cb func(string), stage string) {
	if cb != nil && stage != "" {
		cb(stage)
	}
}

func printCaptchaASCII(data []byte) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		log.Logger.Warnf("[captcha] decode image error: %s", err)
		return
	}
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width == 0 || height == 0 {
		return
	}
	targetWidth := 64
	if width < targetWidth {
		targetWidth = width
	}
	charset := "@%#*+=-:. "
	stepX := float64(width) / float64(targetWidth)
	if stepX < 1 {
		stepX = 1
	}
	stepY := stepX * 2
	var builder strings.Builder
	builder.WriteString("\n[captcha ascii preview]\n")
	for y := 0.0; y < float64(height); y += stepY {
		for x := 0.0; x < float64(width); x += stepX {
			c := img.At(int(x), int(y))
			r, g, b, _ := c.RGBA()
			gray := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
			if gray < 0 {
				gray = 0
			}
			if gray > 1 {
				gray = 1
			}
			index := int(gray * float64(len(charset)-1))
			builder.WriteByte(charset[index])
		}
		builder.WriteByte('\n')
	}
	fmt.Println(builder.String())
}

func promptCaptchaInput() (string, bool) {
	captchaPromptMu.Lock()
	defer captchaPromptMu.Unlock()
	fmt.Print("[captcha] 请输入验证码（直接回车跳过）：")
	reader := bufio.NewReader(os.Stdin)
	text, err := reader.ReadString('\n')
	if err != nil {
		log.Logger.Warnf("[captcha] 读取输入失败: %s", err)
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, true
}

func (lp *LoginAutoData) fillCaptchaInput(page *rod.Page, code string) bool {
	if code == "" || page == nil {
		return false
	}
	inputs, err := page.Elements("input")
	if err != nil {
		return false
	}
	for _, input := range inputs {
		typ := attrLower(input, "type")
		if typ != "" && typ != "text" && typ != "tel" && typ != "number" {
			continue
		}
		if matchKeywords(input, captchaKeywords) || strings.Contains(attrLower(input, "placeholder"), "验证码") {
			input.MustSelectAllText()
			input.Input(code)
			return true
		}
	}
	return false
}

func containsSliderKeyword(html string) bool {
	for _, kw := range sliderKeywords {
		if strings.Contains(html, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
