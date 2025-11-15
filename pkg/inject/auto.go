package inject

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/static"
	"argo/pkg/utils"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

var AutoJsTemplate = `
function run(){
    const sleep = (ms) => new Promise(res => setTimeout(res, ms));
    const skipTags = ["HTML","HEAD","META","TITLE","STYLE","SCRIPT"];
    const username = "%s";
    const password = "%s";
    const email = "%s";
    const phone = "%s";
    const slow = %f;
    const filter = ["%s"].filter(Boolean);
    const actionLimit = 200;
    const queuedUrls = new Set();
    const visited = new WeakSet();
    const queue = [];
    let actions = 0;

    const shouldSkip = (node) => {
        if (!node || !node.tagName) return true;
        if (visited.has(node)) return true;
        return skipTags.indexOf(node.tagName) >= 0;
    };

    const enqueueNode = (node) => {
        if (shouldSkip(node)) return;
        visited.add(node);
        queue.push(node);
    };

    const enqueueUrl = (url) => {
        if (!url) return;
        queuedUrls.add(url);
    };

    const matchesFilter = (node) => {
        const html = node.outerHTML ? node.outerHTML.toLowerCase() : "";
        return filter.some(f => html.includes(f));
    };

    const triggerInput = (node, value) => {
        node.focus && node.focus();
        node.value = value;
        node.dispatchEvent(new Event('input', {bubbles:true}));
        node.dispatchEvent(new Event('change', {bubbles:true}));
    };

    const isClickable = (node) => {
        if (!node) return false;
        const tag = node.tagName;
        if (tag === 'BUTTON') return true;
        if (tag === 'A') return true;
        if (tag === 'INPUT' && ['button','submit'].includes(node.type)) return true;
        if (node.getAttribute('role') === 'button') return true;
        if (node.onclick || node.getAttribute('data-click') || node.getAttribute('ng-click')) return true;
        return false;
    };

    const clickNode = async (node) => {
        if (!node || matchesFilter(node)) return;
        node.focus && node.focus();
        node.dispatchEvent(new MouseEvent('pointerdown', {bubbles:true}));
        node.dispatchEvent(new MouseEvent('mousedown', {bubbles:true}));
        node.click && node.click();
        node.dispatchEvent(new MouseEvent('mouseup', {bubbles:true}));
        node.dispatchEvent(new MouseEvent('pointerup', {bubbles:true}));
        await sleep(slow);
    };

    const processNode = async (node) => {
        if (!node) return;
        if (node.tagName === 'A') {
            const href = node.getAttribute('href');
            if (href && !href.startsWith('javascript') && href !== '#') {
                enqueueUrl(href);
            }
        }
        if (node.tagName === 'INPUT') {
            const type = node.type || 'text';
            if (['text','search','url','password'].includes(type)) {
                triggerInput(node, username);
            } else if (type === 'email') {
                triggerInput(node, email);
            } else if (type === 'tel') {
                triggerInput(node, phone);
            }
        }
        if (isClickable(node)) {
            await clickNode(node);
        }
    };

    const observer = new MutationObserver((mutations) => {
        mutations.forEach((mutation) => {
            mutation.addedNodes && mutation.addedNodes.forEach(enqueueNode);
        });
    });
    observer.observe(document, {subtree:true, childList:true});

    const walker = document.createTreeWalker(document, NodeFilter.SHOW_ELEMENT, null);
    while (walker.nextNode()) {
        enqueueNode(walker.currentNode);
    }

    async function auto(){
        while (queue.length && actions < actionLimit) {
            const node = queue.shift();
            try {
                await processNode(node);
            } catch(err) {
                console.log('auto error', err);
            }
            actions++;
        }
        observer.disconnect();
        return Array.from(queuedUrls);
    }
    return auto();
}
`

func Auto(page *rod.Page) []string {
	hrefList := []string{}
	content := fmt.Sprintf(
		AutoJsTemplate,
		conf.GlobalConfig.LoginConf.Username,
		conf.GlobalConfig.LoginConf.Password,
		conf.GlobalConfig.LoginConf.Email,
		conf.GlobalConfig.LoginConf.Phone,
		conf.GlobalConfig.AutoConf.Slow,
		strings.Join(conf.GlobalConfig.AutoConf.Filter, "\", \""))
	info, err := utils.GetPageInfoByPage(page)
	if err != nil {
		return nil
	}
	log.Logger.Debugf("run auto js %s", info.URL)
	hrefArrays, err := page.Eval(content)

	if err != nil {
		log.Logger.Debugf("Auto run error: %s", err)
		return []string{info.URL}
	}
	for _, url := range hrefArrays.Value.Arr() {
		// log.Logger.Debugf("{auto get href} : %s", url.String())
		hrefList = append(hrefList, url.String())
	}
	return static.HandlerUrls(hrefList, info.URL)
}
