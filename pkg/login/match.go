package login

import (
	"argo/pkg/log"
	"strings"

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

func (lp *LoginAutoData) matchLoginUsername() []*rod.Element {
	// input type=text placeholder= 账号 用户
	usernameElementList := []*rod.Element{}
	inputs, err := lp.Page.Elements("input")
	if err != nil {
		log.Logger.Warnf("matchLoginUsername err: %s", err)
	}
	for _, input := range inputs {
		eType, err := input.Attribute("type")
		eName, err := input.Attribute("name")
		if err != nil || eType == nil {
			continue
		}
		ePlaceholder, _ := input.Attribute("placeholder")
		if *eType != "text" || ePlaceholder == nil {
			continue
		}
		lowEPlaceholder := strings.ToLower(*ePlaceholder)

		if ePlaceholder == nil && eName != nil {
			for _, um := range usernameMatchList {
				if strings.Contains(lowEPlaceholder, um) {
					usernameElementList = append(usernameElementList, input)
				}
			}
		} else {
			for _, um := range usernameMatchList {
				if strings.Contains(lowEPlaceholder, um) {
					usernameElementList = append(usernameElementList, input)
				}
			}
		}
	}
	return usernameElementList
}

func (lp *LoginAutoData) matchLoginEmail() []*rod.Element {
	// input type=text placeholder= 邮箱 email
	emailElementList := []*rod.Element{}
	inputs, err := lp.Page.Elements("input")
	if err != nil {
		log.Logger.Warnf("matchLoginemail err: %s", err)
	}
	for _, input := range inputs {
		eType, err := input.Attribute("type")
		eName, err := input.Attribute("name")
		if err != nil || eType == nil {
			continue
		}
		ePlaceholder, _ := input.Attribute("placeholder")
		if *eType != "text" && *eType != "email" || ePlaceholder == nil {
			continue
		}
		if *eType == "email" {
			emailElementList = append(emailElementList, input)
			continue
		}
		lowEPlaceholder := strings.ToLower(*ePlaceholder)

		if ePlaceholder == nil && eName != nil {
			// 不存在提示 但是存在name name = email/mail 这类
			for _, um := range emailMatchList {
				if strings.Contains(*eName, um) {
					emailElementList = append(emailElementList, input)
				}
			}
		} else {
			for _, um := range emailMatchList {
				if strings.Contains(lowEPlaceholder, um) {
					emailElementList = append(emailElementList, input)
				}
			}
		}
	}
	return emailElementList
}

func (lp *LoginAutoData) matchLoginPhone() []*rod.Element {
	// input type=text placeholder= 手机号 电话号 phone
	phoneElementList := []*rod.Element{}
	inputs, err := lp.Page.Elements("input")
	if err != nil {
		log.Logger.Warnf("matchLoginphone err: %s", err)
	}
	for _, input := range inputs {
		eType, err := input.Attribute("type")
		eName, err := input.Attribute("name")
		if err != nil || eType == nil {
			continue
		}
		ePlaceholder, _ := input.Attribute("placeholder")
		if *eType != "text" && *eType != "tel" || ePlaceholder == nil {
			continue
		}
		if *eType == "tel" {
			phoneElementList = append(phoneElementList, input)
			continue
		}

		lowEPlaceholder := strings.ToLower(*ePlaceholder)

		if ePlaceholder == nil && eName != nil {
			for _, um := range phoneMatchList {
				if strings.Contains(*eName, um) {
					phoneElementList = append(phoneElementList, input)

				}
			}
		} else {
			for _, um := range phoneMatchList {
				if strings.Contains(lowEPlaceholder, um) {
					phoneElementList = append(phoneElementList, input)
				}
			}
		}
	}
	return phoneElementList
}

func (lp *LoginAutoData) matchLoginPassword() []*rod.Element {
	// input type=password placeholder = 密码 password
	passwordElementList := []*rod.Element{}
	inputs, err := lp.Page.Elements("input")
	if err != nil {
		log.Logger.Warnf("matchLoginpassword err: %s", err)
	}
	for _, input := range inputs {
		eType, err := input.Attribute("type")
		if err != nil || eType == nil {
			continue
		}
		ePlaceholder, _ := input.Attribute("placeholder")
		if *eType != "password" || ePlaceholder == nil {
			continue
		}
		lowEPlaceholder := strings.ToLower(*ePlaceholder)

		if ePlaceholder == nil {
			passwordElementList = append(passwordElementList, input)
		} else {
			for _, um := range passwordMatchList {
				if strings.Contains(lowEPlaceholder, um) {
					passwordElementList = append(passwordElementList, input)
				}
			}
		}
	}
	return passwordElementList
}

func (lp *LoginAutoData) matchLoginVerifCode() {

}

func (lp *LoginAutoData) matchLoginSubmit() []*rod.Element {
	// button type=button/submit text 包含登录
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
	return submitElementList
}

func (lp *LoginAutoData) tryLogin() {
	usernameElementList := lp.matchLoginUsername()
	passwordElementList := lp.matchLoginPassword()
	emailElementList := lp.matchLoginEmail()
	phoneElementList := lp.matchLoginPhone()
	// lp.matchLoginVerifCode()
	submitElementList := lp.matchLoginSubmit()
	// 输入用户名
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
	for _, se := range submitElementList {
		// se.MustClick()
		// se.Click()
		// pointer-events: none;
		pointerEvent, _ := se.Eval("()=>window.getComputedStyle(this,null).getPropertyValue('pointer-events')")
		if pointerEvent == nil {
			continue
		}
		if pointerEvent.Value.String() == "none" {
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
					log.Logger.Debugf("点击登录按钮失败 %s", err)
				}
			}
		} else {
			log.Logger.Debug("尝试点击登录按钮")
			err := se.Click(proto.InputMouseButtonLeft, 1)
			if err != nil {
				log.Logger.Debugf("点击登录按钮失败 %s", err)
			}
		}
	}
}
