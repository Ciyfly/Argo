()=>{
    var links = document.getElementsByTagName("a");
// 循环遍历链接元素，添加点击事件处理程序
    for (var i = 0; i < links.length; i++) {
    links[i].addEventListener("click", function(event) {
        console.log(event.target.href)
        // 禁止页面的新开 href的那种 对于href是javascript的就正常执行
        if (event.target.href.indexOf("javascript")==-1 || this.target === '_blank') {
            event.preventDefault();
        }
        });
    }
}