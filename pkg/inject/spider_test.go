package inject

import (
	"testing"
	"time"

	"argo/pkg/conf"
	"argo/pkg/log"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// 无头环境验证蜘蛛叠加层的三要素隔离与可用性：
//  1. closed shadow 内部对 document.querySelectorAll 不可见（不污染可点元素/链接扫描）
//  2. 蜘蛛移动不触发外层 MutationObserver（不干扰 settleAfterClick 的页面变化判定）
//  3. tap() 可 resolve 且真实点击照常发生
func TestSpiderOverlayIsolation(t *testing.T) {
	conf.GlobalConfig = &conf.Conf{}
	conf.GlobalConfig.AutoConf.Spider = true
	log.Init(false, false)

	ctrl := launcher.New().Bin("/usr/bin/chromium-browser").NoSandbox(true).Headless(true).MustLaunch()
	browser := rod.New().ControlURL(ctrl).MustConnect()
	defer browser.MustClose()
	page := browser.MustPage("data:text/html,<button id='b' onclick='this.dataset.hit=1'>x</button><a href='/l'>l</a>")
	page.MustWaitLoad()

	if res := page.MustEval(spiderOverlayJS); !res.Bool() {
		t.Fatalf("蜘蛛叠加层注入失败")
	}

	// 1. light DOM 查询看不见蜘蛛内部
	count := page.MustEval(`() => document.querySelectorAll(".spider,.leg,.argo-spider-host *").length`).Int()
	if count != 0 {
		t.Errorf("closed shadow 内部元素泄漏到 light DOM 查询: %d", count)
	}
	// host 存在但无内容无属性（HTML 序列化不含蜘蛛）
	hostInfo := page.MustEval(`() => {
		const h = document.getElementById("argo-spider-host");
		return h ? h.outerHTML.length : -1;
	}`).Int()
	if hostInfo < 0 || hostInfo > 200 {
		t.Errorf("host 元素应是无内容空壳, outerHTML len=%d", hostInfo)
	}

	// 2. 蜘蛛动画不影响骨架哈希判定（closed shadow 内的结构不进 visible DOM 骨架）
	page.MustEval(armStateJS)
	page.MustEval(`() => window.__argoSpider.moveTo(100, 100, 120)`)
	time.Sleep(300 * time.Millisecond)
	mutated := page.MustEval(stateSettledJS).Str() != ""
	if mutated {
		t.Errorf("蜘蛛移动影响了骨架哈希判定，会干扰交互后的页面变化判定")
	}

	// 3. tap 可 resolve 且真实点击生效
	done := page.MustEval(`async () => {
		const b = document.getElementById("b");
		const r = b.getBoundingClientRect();
		await window.__argoSpider.tap(r.left + r.width / 2, r.top + r.height / 2);
		b.click();
		return b.dataset.hit === "1";
	}`)
	if !done.Bool() {
		t.Errorf("tap 后真实点击未生效")
	}
}
