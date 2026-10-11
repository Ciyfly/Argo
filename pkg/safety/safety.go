// Package safety 危险操作防护。
//
// 爬虫的默认交互（填表、提交、开 tab）会修改目标状态（改密码/删除/登出自己
// 的会话，DVWA 实测见 docs/safety-guard-design.md §1.1）。本包提供两道防线：
//
//  1. URL 防线：ShouldSkipURL——登出/会话销毁类 URL 不开 tab，
//     但仍记录为「发现未请求」结果（URL 发现不受损）；
//  2. 表单防线：FormSkipPayload——把提交黑名单正则注入 submitFormsJS，
//     命中的表单不自动提交。
//
// 另有站点指纹识别（ObserveHTML）：命中 DVWA 等已知站点时叠加 strict 策略
// （额外拦设置类表单）。「security/settings」在真实站点过宽不能默认拦，
// 降为指纹触发 + 可手动开启（profile: strict），符合特解兜底规范。
package safety

import (
	"argo/pkg/log"
	"encoding/json"
	"regexp"
	"strings"
	"sync/atomic"
)

// profileStrict 是否处于 strict 策略档案（指纹命中或手动指定）。
// 写入只有初始化/首次命中各一次，读多写少，atomic 足够。
var profileStrict atomic.Bool

// safetyEnabled 总开关；false 时所有判定直接放行。
var safetyEnabled atomic.Bool

// autoProfileOn 站点指纹识别开关（enabled 且 auto_profile 时才生效）。
var autoProfileOn atomic.Bool

// urlSkipRes URL 黑名单正则（对完整 URL 小写匹配）。
var urlSkipRes []*regexp.Regexp

// formSkipRes 表单提交黑名单正则（对 form outerHTML 小写匹配）。
var formSkipRes []*regexp.Regexp

// strictFormSkipRes strict 档案追加的设置类表单正则（内置，不开放配置）。
var strictFormSkipRes = compileRes([]string{
	"security|phpids|安全设置",
})

// knownSiteProfiles 已知站点指纹 → 叠加档案。
// 子串匹配对页面 HTML 小写后进行；裸 "dvwa" 误报率高，用完整短语/带符号形式。
var knownSiteProfiles = []struct {
	Fingerprints []string
	Profile      string
}{
	{Fingerprints: []string{"damn vulnerable web application", ">dvwa<"}, Profile: "strict"},
}

// compileRes 编译正则列表，非法项 Fatal（调用方在启动时传入配置，
// 用户第一时间知道哪条写错，与 form.rules 同路径）。
func compileRes(patterns []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			if log.Logger != nil {
				log.Logger.Fatalf("safety regex invalid: %q err: %s", pattern, err)
			}
			panic("safety regex invalid: " + pattern + ": " + err.Error())
		}
		out = append(out, re)
	}
	return out
}

// Init 启动时初始化（engine 启动路径调用一次）。
func Init(enabled bool, autoProfile bool, profile string, urlSkip []string, formSubmitSkip []string) {
	safetyEnabled.Store(enabled)
	if !enabled {
		return
	}
	autoProfileOn.Store(autoProfile)
	urlSkipRes = compileRes(urlSkip)
	formSkipRes = compileRes(formSubmitSkip)
	if strings.EqualFold(profile, "strict") {
		profileStrict.Store(true)
		if log.Logger != nil {
			log.Logger.Info("safety profile: strict (manual)")
		}
		return
	}
	if !autoProfile {
		return
	}
	// autoProfile 时 strict 由 ObserveHTML 按页面指纹触发
}

// ShouldSkipURL 判断 URL 是否不该打开（登出/会话销毁类）。
// 命中：调用方仍应把 URL 记为「发现未请求」结果。
func ShouldSkipURL(rawURL string) bool {
	if !safetyEnabled.Load() {
		return false
	}
	low := strings.ToLower(rawURL)
	for _, re := range urlSkipRes {
		if re.MatchString(low) {
			return true
		}
	}
	return false
}

// IsStrict 当前是否 strict 档案。
func IsStrict() bool {
	return safetyEnabled.Load() && profileStrict.Load()
}

// ObserveHTML 站点指纹识别：页面 HTML 命中已知站点时叠加对应档案。
// NewTab 每页调用；进程内首次命中后即固定，无锁（原子写幂等）。
func ObserveHTML(html string) {
	if !safetyEnabled.Load() || !autoProfileOn.Load() || profileStrict.Load() {
		return
	}
	low := strings.ToLower(html)
	for _, kp := range knownSiteProfiles {
		for _, fp := range kp.Fingerprints {
			if strings.Contains(low, fp) {
				profileStrict.Store(true)
				if log.Logger != nil {
					log.Logger.Infof("safety profile: strict (fingerprint %q)", kp.Profile)
				}
				return
			}
		}
	}
}

// FormSkipPayload 把当前生效的表单提交黑名单序列化成 JS 侧参数。
// strict 档案追加内置设置类正则。disabled 返回空列表（JS 侧零跳过）。
func FormSkipPayload() string {
	patterns := []string{}
	if safetyEnabled.Load() {
		for _, re := range formSkipRes {
			patterns = append(patterns, re.String())
		}
		if profileStrict.Load() {
			for _, re := range strictFormSkipRes {
				patterns = append(patterns, re.String())
			}
		}
	}
	data, err := json.Marshal(map[string][]string{"skip": patterns})
	if err != nil {
		// 纯字符串列表序列化不会失败，防御兜底
		return `{"skip":[]}`
	}
	return string(data)
}
