()=>{
    var linksa = document.getElementsByTagName("a");
    var linkm = document.getElementsByTagName("form");
    var linkb = document.getElementsByTagName("button");
    var linki = document.getElementsByTagName("input");
    var mergedArray = [...linksa, ...linkm, ...linkb, ...linki];
// 循环遍历链接元素，添加点击事件处理程序
    for (var i = 0; i < mergedArray.length; i++) {
    if (mergedArray[i].hasAttribute("target")){
        mergedArray[i].setAttribute("target","_self")
    }
    mergedArray[i].addEventListener("click", function(event) {
        // 禁止页面的新开 href的那种 对于href是javascript的就正常执行
        if (event.target.href.indexOf("javascript")==-1 ) {
            event.preventDefault();
        }
        });
    }
}