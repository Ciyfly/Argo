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
            if (node.tagName=="INPUT" &&  node.type=="text" || node.tagName=="INPUT" &&  node.type=="password"  || node.tagName=="INPUT" &&  node.type=="email"  || node.tagName=="INPUT" &&  node.type=="tel"){
                console.log(node.type)
                if (node.type=="text"){
                    node.textContent = username
                    node.nodeValue = username
                    node.setRangeText(username)
                }else if(node.type=="password") {
                    node.textContent = password
                    node.nodeValue = password
                    node.setRangeText(password)
                }else if (node.type=="email"){
                    node.textContent = email
                    node.nodeValue = email
                    node.setRangeText(email)
                }else if (node.type=="tel"){
                    node.textContent = phone
                    node.nodeValue = phone
                    node.setRangeText(phone)
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
