package inject

import (
	"argo/pkg/conf"
	"strings"
	"testing"
)

// 回归测试：注入的自动填表 JS 必须通过原生 value setter 给 input 赋值。
//
// 历史上用的是 node.textContent / node.nodeValue / node.setRangeText，
// 这三个对 input 都改不了 value（textContent 只影响元素子节点，
// setRangeText 只对选中文本生效），所以自动填表实际什么都没填进去。
func TestAutoJsTemplateSetsInputValue(t *testing.T) {
	// 不允许再出现这三种错误的赋值方式
	for _, bad := range []string{
		"node.textContent = username",
		"node.nodeValue = username",
		"node.setRangeText(username)",
		"node.setRangeText(password)",
		"node.setRangeText(email)",
		"node.setRangeText(phone)",
	} {
		if strings.Contains(AutoJsTemplate, bad) {
			t.Errorf("注入 JS 里仍有无效的赋值方式: %s", bad)
		}
	}

	// 必须存在通过 value setter 赋值的实现
	if !strings.Contains(AutoJsTemplate, "Object.getOwnPropertyDescriptor") {
		t.Errorf("缺少通过原型 value setter 赋值的实现，框架类页面的输入框不会被识别")
	}
	if !strings.Contains(AutoJsTemplate, `dispatchEvent(new Event("input"`) {
		t.Errorf("赋值后没有派发 input 事件，依赖事件监听的页面感知不到输入")
	}
	if !strings.Contains(AutoJsTemplate, `dispatchEvent(new Event("change"`) {
		t.Errorf("赋值后没有派发 change 事件")
	}
	if !strings.Contains(AutoJsTemplate, "function setInputValue(") {
		t.Errorf("缺少 setInputValue 辅助函数")
	}
}

// 回归测试：四种输入框类型都要被覆盖到。
func TestAutoJsTemplateCoversInputTypes(t *testing.T) {
	for _, typ := range []string{"text", "password", "email", "tel"} {
		if !strings.Contains(AutoJsTemplate, `node.type=="`+typ+`"`) {
			t.Errorf("注入 JS 没有处理 type=%s 的输入框", typ)
		}
	}
}

// 回归测试：用户名/密码等占位符必须都被模板参数填满，
// 否则 Auto() 里的 Sprintf 会多出 %!(EXTRA ...) 之类的脏字符。
func TestAutoJsTemplatePlaceholdersMatch(t *testing.T) {
	// 4 个字符串（用户名/密码/邮箱/手机号）+ 1 个 float（slow）+ 1 个字符串（filter）
	want := 6
	if got := strings.Count(AutoJsTemplate, "%s") + strings.Count(AutoJsTemplate, "%f"); got != want {
		t.Errorf("模板占位符数量应为 %d，实际 %d", want, got)
	}

	conf.GlobalConfig = &conf.Conf{}
	conf.GlobalConfig.LoginConf.Username = "user1"
	conf.GlobalConfig.LoginConf.Password = "pass1"
	conf.GlobalConfig.LoginConf.Email = "e@example.com"
	conf.GlobalConfig.LoginConf.Phone = "13800000000"
	conf.GlobalConfig.AutoConf.Slow = 1000
	conf.GlobalConfig.AutoConf.Filter = []string{"logout", "登出"}

	rendered := renderAutoJs(conf.GlobalConfig)
	if strings.Contains(rendered, "%!") {
		t.Errorf("渲染后的 JS 存在未填充的占位符: %s", firstLineWith(rendered, "%!"))
	}
	for _, want := range []string{`"user1"`, `"pass1"`, `"e@example.com"`, `"13800000000"`} {
		if !strings.Contains(rendered, want) {
			t.Errorf("渲染后的 JS 缺少 %s", want)
		}
	}
}

func firstLineWith(s, sub string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}
