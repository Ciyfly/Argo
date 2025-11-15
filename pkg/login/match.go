package login

import (
	"fmt"
	"strings"

	"argo/pkg/log"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

var usernameMatchList = []string{
	"user",
	"name",
	"账号",
	"用户",
}
var emailMatchList = []string{
	"mail",
	"email",
	"邮箱",
}
var phoneMatchList = []string{
	"phone",
	"手机",
	"电话",
}
var passwordMatchList = []string{
	"passwd",
	"password",
	"密码",
}

var submitMatchList = []string{
	"登录",
	"login",
	"提交",
}

var attributeCandidates = []string{
	"placeholder",
	"name",
	"aria-label",
	"id",
	"class",
	"data-placeholder",
	"data-label",
	"data-login",
	"autocomplete",
	"value",
}

func attrLower(el *rod.Element, attr string) string {
	if el == nil {
		return ""
	}
	val, err := el.Attribute(attr)
	if err != nil || val == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*val))
}

func matchKeywords(el *rod.Element, keywords []string) bool {
	for _, attr := range attributeCandidates {
		content := attrLower(el, attr)
		if content == "" {
			continue
		}
		for _, kw := range keywords {
			if strings.Contains(content, strings.ToLower(kw)) {
				return true
			}
		}
	}
	return false
}

func describeElement(el *rod.Element) string {
	if el == nil {
		return ""
	}
	placeholder := attrLower(el, "placeholder")
	nameAttr := attrLower(el, "name")
	idAttr := attrLower(el, "id")
	classAttr := attrLower(el, "class")
	return fmt.Sprintf("placeholder=%q name=%q id=%q class=%q", placeholder, nameAttr, idAttr, classAttr)
}

func (lp *LoginAutoData) matchLoginUsername() []*rod.Element {
	usernameElementList := []*rod.Element{}
	inputs, err := lp.Page.Elements("input")
	if err != nil {
		log.Logger.Warnf("matchLoginUsername err: %s", err)
		return usernameElementList
	}
	for _, input := range inputs {
		typ := attrLower(input, "type")
		if typ == "" {
			typ = "text"
		}
		if typ != "text" && typ != "search" && typ != "number" {
			continue
		}
		if matchKeywords(input, usernameMatchList) || attrLower(input, "autocomplete") == "username" {
			usernameElementList = append(usernameElementList, input)
			continue
		}
	}
	return usernameElementList
}

func (lp *LoginAutoData) matchLoginEmail() []*rod.Element {
	emailElementList := []*rod.Element{}
	inputs, err := lp.Page.Elements("input")
	if err != nil {
		log.Logger.Warnf("matchLoginemail err: %s", err)
		return emailElementList
	}
	for _, input := range inputs {
		typ := attrLower(input, "type")
		if typ != "email" && typ != "text" {
			continue
		}
		if typ == "email" {
			emailElementList = append(emailElementList, input)
			continue
		}
		if matchKeywords(input, emailMatchList) {
			emailElementList = append(emailElementList, input)
		}
	}
	return emailElementList
}

func (lp *LoginAutoData) matchLoginPhone() []*rod.Element {
	phoneElementList := []*rod.Element{}
	inputs, err := lp.Page.Elements("input")
	if err != nil {
		log.Logger.Warnf("matchLoginphone err: %s", err)
		return phoneElementList
	}
	for _, input := range inputs {
		typ := attrLower(input, "type")
		if typ != "tel" && typ != "text" && typ != "number" {
			continue
		}
		if typ == "tel" {
			phoneElementList = append(phoneElementList, input)
			continue
		}
		if matchKeywords(input, phoneMatchList) || attrLower(input, "autocomplete") == "tel" {
			phoneElementList = append(phoneElementList, input)
		}
	}
	return phoneElementList
}

func (lp *LoginAutoData) matchLoginPassword() []*rod.Element {
	passwordElementList := []*rod.Element{}
	inputs, err := lp.Page.Elements("input")
	if err != nil {
		log.Logger.Warnf("matchLoginpassword err: %s", err)
		return passwordElementList
	}
	for _, input := range inputs {
		typ := attrLower(input, "type")
		if typ == "password" {
			passwordElementList = append(passwordElementList, input)
			continue
		}
		if matchKeywords(input, passwordMatchList) {
			passwordElementList = append(passwordElementList, input)
		}
	}
	return passwordElementList
}

func (lp *LoginAutoData) matchLoginVerifCode() {

}

func (lp *LoginAutoData) matchLoginSubmit() []*rod.Element {
	submitElementList := []*rod.Element{}
	buttons, err := lp.Page.Elements("button")
	if err != nil {
		log.Logger.Warnf("matchLoginsubmit err: %s", err)
	}
	for _, button := range buttons {
		eType, err := button.Attribute("type")
		if err != nil || eType == nil {
			continue
		}
		buttonHtml, _ := button.HTML()
		if *eType == "submit" {
			submitElementList = append(submitElementList, button)
			continue
		}
		lowButtonHtml := strings.ToLower(buttonHtml)
		for _, um := range submitMatchList {
			if strings.Contains(lowButtonHtml, um) {
				submitElementList = append(submitElementList, button)
			}
		}
	}
	inputButtons, err := lp.Page.Elements("input")
	if err == nil {
		for _, input := range inputButtons {
			typ := attrLower(input, "type")
			if typ != "submit" && typ != "button" {
				continue
			}
			if matchKeywords(input, submitMatchList) || attrLower(input, "value") == "登录" {
				submitElementList = append(submitElementList, input)
			}
		}
	}
	return submitElementList
}

func (lp *LoginAutoData) tryLogin(stageRecorder func(string)) {
	usernameElementList := lp.matchLoginUsername()
	passwordElementList := lp.matchLoginPassword()
	emailElementList := lp.matchLoginEmail()
	phoneElementList := lp.matchLoginPhone()
	submitElementList := lp.matchLoginSubmit()

	log.Logger.Debugf("[login match] username:%d password:%d email:%d phone:%d submit:%d",
		len(usernameElementList), len(passwordElementList), len(emailElementList), len(phoneElementList), len(submitElementList))
	for _, el := range usernameElementList {
		log.Logger.Debugf("[login match] username field %s", describeElement(el))
	}
	for _, el := range passwordElementList {
		log.Logger.Debugf("[login match] password field %s", describeElement(el))
	}
	if len(passwordElementList) == 0 || len(submitElementList) == 0 {
		log.Logger.Warn("[login] 未找到密码或提交控件，跳过自动登录")
		recordStage(stageRecorder, "interaction:login:missing_fields")
		return
	}

	recordStage(stageRecorder, "interaction:login:fill_fields")
	for _, ue := range usernameElementList {
		ue.Input(lp.Username)
	}
	for _, pe := range passwordElementList {
		pe.Input(lp.Password)
	}
	for _, em := range emailElementList {
		em.Input(lp.Email)
	}
	for _, ph := range phoneElementList {
		ph.Input(lp.Phone)
	}
	recordStage(stageRecorder, "interaction:login:click_submit")
	for _, se := range submitElementList {
		// se.MustClick()
		// se.Click()
		// pointer-events: none;
		pointerEvent := se.MustEval("()=>window.getComputedStyle(this,null).getPropertyValue('pointer-events')")
		if pointerEvent.String() == "none" {
			log.Logger.Debug("登录按钮存在 pointer-events 尝试点击子元素")
			// 对子元素进行点击
			children, err := se.Elements("")
			if err != nil {
				continue
			}
			for _, c := range children {
				log.Logger.Debug("尝试点击登录按钮")
				err := c.Click(proto.InputMouseButtonLeft, 1)
				if err != nil {
					log.Logger.Warnf("点击登录按钮失败 %s", err)
				}
			}
		} else {
			log.Logger.Debug("尝试点击登录按钮")
			err := se.Click(proto.InputMouseButtonLeft, 1)
			if err != nil {
				log.Logger.Warnf("点击登录按钮失败 %s", err)
			}
		}
	}
}
