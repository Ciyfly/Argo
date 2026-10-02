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
    function sleep(ms) {
        return new Promise(res => setTimeout(res, ms));
    }
    var NodeArrays = new Array();
    var HrefArrays = new Array();
    var FilterTags = ["HTML", "HEAD", "META", "TITLE", "LINK", "STYLE", "IMG", "DIV", "SCRIPT"];
    var username = "%s";
    var password = "%s";
    var email = "%s"
    var phone = "%s";
    var slow = %f;
    var filter = ["%s"];

    // 判断是否是过滤的 不包含过滤字符串才进行点击
    function filterClick(node){
        var lowText = node.outerHTML.toLowerCase()
        for (const f of filter) {
            if (lowText.includes(f)){
                 console.log("filter -> ",lowText)
                return
            }
        }
        console.log("click -> ",lowText)
        node.click();
    }

    // 给输入框赋值
    // fix: 原先用 node.textContent / node.nodeValue / node.setRangeText 赋值，
    // 这三个对 input/textarea 都改不了 value（textContent 只影响元素子节点，
    // setRangeText 只对选中文本生效），所以自动填表实际什么都没填进去。
    // 必须走原生 value 的 setter，并派发 input/change 事件，
    // React/Vue 这类框架也是靠覆写 value setter 来感知输入的。
    function setInputValue(node, value){
        var proto = node.tagName === "TEXTAREA" ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
        var descriptor = Object.getOwnPropertyDescriptor(proto, "value");
        if (descriptor && descriptor.set) {
            descriptor.set.call(node, value);
        } else {
            node.value = value;
        }
        node.dispatchEvent(new Event("input", {bubbles: true}));
        node.dispatchEvent(new Event("change", {bubbles: true}));
        console.log("input -> ", node.type, node.value)
    }

    // 需要自动填值的输入框类型
    function isFillableInput(node){
        if (node.tagName !== "INPUT"){
            return false
        }
        return ["text", "password", "email", "tel"].indexOf(node.type) >= 0
    }
    
    function treeWalkerFilter(element) {
        if (element.nodeType === Node.ELEMENT_NODE) {
            return NodeFilter.FILTER_ACCEPT;
        }
    }
    function nodeRecur(ch){
        for(var i=0;i<ch.length;i++){
            if (FilterTags.indexOf(ch[i].tagName)<0){
                NodeArrays.unshift(ch[i])
            }
            
            if(ch[i].children.length>0){
                nodeRecur(ch[i].children)
            }
        }
    }

    async function auto (){
        treeWalker = document.createTreeWalker(
            document,
            NodeFilter.SHOW_ELEMENT,
            treeWalkerFilter,
            false
        );
        var observer = new MutationObserver(function(mutations ){
            mutations.forEach(function (mutation) {
                if (mutation.type === 'childList') {
                    // 在创建新的 element 时调用
                    console.log("child append ", mutation.target);
                    nodeRecur(mutation.target.children)
                } else if (mutation.type === 'attributes') {
                    // 在属性发生变化时调用
                    console.log("attributes: ");
                    console.log(mutation);
                }
            });
        });
        
        observer.observe(window.document, {
            subtree: true,
            childList: true,
            characterData: true,
            attributes: true,
            attributeFilter: ['src', 'href', 'action']
        });
        
        
        while (treeWalker.nextNode()) {
            if (treeWalker.currentNode.tagName==null){
                continue
            }
            if (FilterTags.indexOf(treeWalker.currentNode.tagName)<0) {
                NodeArrays.push(treeWalker.currentNode)
            } 
        }
        
        while (NodeArrays.length!=0){
            var node = NodeArrays.shift();
            console.log(node.tagName)
            console.log("NodeArrays len: ", NodeArrays.length)
            if (node==null){
                continue
            }
            node.style.color="red";
            // 如果是input 输入的也要先判断是什么类型的然后输入
            if (isFillableInput(node)){
                console.log(node.type)
                if (node.type=="text"){
                    setInputValue(node, username)
                }else if(node.type=="password") {
                    setInputValue(node, password)
                }else if (node.type=="email"){
                    setInputValue(node, email)
                }else if (node.type=="tel"){
                    setInputValue(node, phone)
                }

            }else if (node.tagName == "A"){
                // A标签有url
                if (node.attributes.href && node.attributes.href.nodeValue){
                    console.log(node.attributes.href.nodeValue)
                    if (node.attributes.href.nodeValue.indexOf("javascript")==-1 && node.attributes.href.nodeValue!="#"){
                        // url
                        console.log("push -> ",node.attributes.href.nodeValue)
                        HrefArrays.push(node.attributes.href.nodeValue)
                    }else{
                        // javascript
                        filterClick(node);
                        await sleep(slow);
                    }
                }

            }else if (node.tagName=="INPUT" &&  node.type=="submit" || node.tagName=="BUTTON" || node.tagName=="INPUT" &&  node.type=="button"){
                filterClick(node);
                await sleep(slow);

            }
        }
        // 返回匹配到所有的url
        console.log(HrefArrays)
        return HrefArrays
    }
    console.log("start run auto")
    return auto ();
}   
`

// renderAutoJs 把配置填充进注入脚本模板。
// 抽成独立函数，既方便测试校验占位符是否都填对，也避免 Auto() 里内联一大段 Sprintf。
func renderAutoJs(c *conf.Conf) string {
	return fmt.Sprintf(
		AutoJsTemplate,
		c.LoginConf.Username,
		c.LoginConf.Password,
		c.LoginConf.Email,
		c.LoginConf.Phone,
		c.AutoConf.Slow,
		strings.Join(c.AutoConf.Filter, "\", \""))
}

func Auto(page *rod.Page) []string {
	hrefList := []string{}
	content := renderAutoJs(conf.GlobalConfig)
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
