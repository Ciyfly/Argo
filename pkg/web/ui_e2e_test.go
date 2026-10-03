package web

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// 这些是端到端集成测试：需要已运行的控制系统台（127.0.0.1:8088）与
// 本地靶场（127.0.0.1:8877，见 crawl_range）。默认跳过，保持单测套件零外部依赖：
//
//	ARGO_WEB_E2E=1 go test ./pkg/web/ -run TestConsoleUI -v
func e2eEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("ARGO_WEB_E2E") != "1" {
		t.Skip("需要外部环境（控制台+靶场），设 ARGO_WEB_E2E=1 启用")
	}
}

// 前端 E2E：真实浏览器驱动控制台页面——新建项目弹层 → 填表 → 创建 →
// 侧栏出现项目且选中（历史上前端用大写 JSON 键导致创建后什么都不显示）。
func TestConsoleUICreateProject(t *testing.T) {
	e2eEnabled(t)
	ctrl := launcher.New().Bin("/usr/bin/chromium-browser").NoSandbox(true).Headless(true).MustLaunch()
	browser := rod.New().ControlURL(ctrl).MustConnect()
	defer browser.MustClose()
	page := browser.MustPage("http://127.0.0.1:8088/")
	page.MustWaitLoad()
	time.Sleep(800 * time.Millisecond)

	// 1. 打开新建弹层并填写
	page.MustEval(`() => {
		document.getElementById('newBtn').click();
		document.getElementById('fName').value = 'UI测试项目';
		document.getElementById('fTargets').value = 'http://127.0.0.1:8877/classic/login.html';
		document.getElementById('fDepth').value = '1';
	}`)
	// 2. 提交创建
	page.MustEval(`() => document.getElementById('dlgOk').click()`)
	// 3. 等待项目出现在侧栏并自动选中
	deadline := time.Now().Add(8 * time.Second)
	sidebarOK, headerOK := false, false
	for time.Now().Before(deadline) {
		html := page.MustEval(`() => document.getElementById('plist').innerHTML`).Str()
		if strings.Contains(html, "UI测试项目") {
			sidebarOK = true
		}
		title := page.MustEval(`() => document.getElementById('ptitle').textContent`).Str()
		if title == "UI测试项目" {
			headerOK = true
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !sidebarOK {
		t.Errorf("侧栏未出现新项目")
	}
	if !headerOK {
		t.Errorf("项目未被自动选中（标题未更新）")
	}
	// 4. 目标状态点已渲染
	targets := page.MustEval(`() => document.getElementById('ptargets').textContent`).Str()
	if !strings.Contains(targets, "classic") && !strings.Contains(targets, "127.0.0.1") {
		t.Errorf("目标状态未渲染: %q", targets)
	}
}

// 前端 E2E：启动项目后 URL 流开始滚动、画面进入直播。
func TestConsoleUIRunProject(t *testing.T) {
	e2eEnabled(t)
	ctrl := launcher.New().Bin("/usr/bin/chromium-browser").NoSandbox(true).Headless(true).MustLaunch()
	browser := rod.New().ControlURL(ctrl).MustConnect()
	defer browser.MustClose()
	page := browser.MustPage("http://127.0.0.1:8088/")
	page.MustWaitLoad()
	time.Sleep(800 * time.Millisecond)

	// 选中第一个项目并启动
	page.MustEval(`() => {
		const first = document.querySelector('.proj');
		if (!first) { throw new Error('no project in sidebar'); }
		first.click();
	}`)
	time.Sleep(500 * time.Millisecond)
	page.MustEval(`() => document.getElementById('startBtn').click()`)

	// URL 列表应在几秒内开始有内容（SSE 推送 + 前端渲染）
	deadline := time.Now().Add(25 * time.Second)
	fed := false
	for time.Now().Before(deadline) {
		count := page.MustEval(`() => document.querySelectorAll('#urls .u').length`).Int()
		if count > 0 {
			fed = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !fed {
		t.Errorf("URL 流没有内容（SSE 或前端渲染失败）")
	}
	// 画面 img 已显示
	display := page.MustEval(`() => document.getElementById('stream').style.display`).Str()
	if display != "block" {
		t.Errorf("浏览器画面未显示, display=%q", display)
	}
	// 停止
	page.MustEval(`() => document.getElementById('stopBtn').click()`)
}

// 前端 E2E：项目跑完后页面能感知——状态徽章变化 + 完成提示弹出。
func TestConsoleUICompletion(t *testing.T) {
	e2eEnabled(t)
	ctrl := launcher.New().Bin("/usr/bin/chromium-browser").NoSandbox(true).Headless(true).MustLaunch()
	browser := rod.New().ControlURL(ctrl).MustConnect()
	defer browser.MustClose()
	page := browser.MustPage("http://127.0.0.1:8088/")
	page.MustWaitLoad()
	time.Sleep(800 * time.Millisecond)

	// 建单目标项目并启动（depth 1，40s 内应结束）
	page.MustEval(`() => {
		document.getElementById('newBtn').click();
		document.getElementById('fName').value = '完成感知测试';
		document.getElementById('fTargets').value = 'http://127.0.0.1:8877/classic/login.html';
		document.getElementById('fDepth').value = '1';
		document.getElementById('dlgOk').click();
	}`)
	time.Sleep(1500 * time.Millisecond)
	page.MustEval(`() => document.getElementById('startBtn').click()`)

	deadline := time.Now().Add(120 * time.Second)
	badgeOK, toastOK := false, false
	for time.Now().Before(deadline) {
		badge := page.MustEval(`() => document.getElementById('pstatus').textContent`).Str()
		if !badgeOK && (badge == "已完成" || badge == "已停止") {
			badgeOK = true
		}
		toastShown := page.MustEval(`() => document.getElementById('toast').style.display`).Str()
		if !toastOK && toastShown == "block" {
			toastOK = true
		}
		if badgeOK && toastOK {
			break
		}
		time.Sleep(1000 * time.Millisecond)
	}
	if !badgeOK {
		t.Errorf("状态徽章未变为已完成/已停止")
	}
	if !toastOK {
		t.Errorf("完成提示未弹出")
	}
}
