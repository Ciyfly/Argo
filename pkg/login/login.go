package login

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/utils"
	"strings"

	"github.com/go-rod/rod"
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

func parse(page *rod.Page) bool {
	if page == nil {
		return false
	}
	html, _ := page.HTML()
	info, err := utils.GetPageInfoByPage(page)
	if err != nil {
		return false
	}
	url := info.URL
	title := info.Title
	if strings.Contains(strings.ToLower(url), "/login") {
		return true
	}
	if strings.Contains(title, "登录") || strings.Contains(strings.ToLower(title), "login") {
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

func (lp *LoginAutoData) Handler(page *rod.Page) {
	// 判断这个页面是否需要登录
	if parse(page) {
		// 需要登录 自动化匹配输入框和提交框
		currentUrl, err := utils.GetCurrentUrlByPage(page)
		if err != nil {
			return
		}
		log.Logger.Debugf("try login %s", currentUrl)
		// 自动匹配输入框和密码框测试登录
		lp.Page = page
		lp.tryLogin()
	} else {
		currentUrl, err := utils.GetCurrentUrlByPage(page)
		if err != nil {
			return
		}
		log.Logger.Debugf("It did not recognize that login was required %s", currentUrl)
	}
}
