# Argo

中文 |  [English ](./README_EN.md)

基于go-rod的自动化通用爬虫 用于自动化获取网站的URL 肯定也是基于无头浏览器实现的

## 功能
支持如下
1. 智能触发页面事件 比如点击后有新增的dom 会优先进行处理
2. 智能登录网站 暂不支持有验证码的情况
3. 支持hook全流量 通过go-rod的 HijackRequests 获取浏览器的全部流量输出请求及响应内容
4. 对URL进行去重 最后输出存储的都是去重后的
5. 支持多格式结果输出 txt、json、xlsx、html
6. 支持 回放yaml格式的脚本 会按照顺序执行操作
7. 支持开启浏览器界面 支持debug输出
8. 支持代理


## 安装

可以直接从这里下载最新版 https://github.com/Ciyfly/Argo/releases
不需要手动下载 chrome 直接运行程序会自动下载chrome

```yaml
./argo -h
NAME:
   argo -  -t http://testphp.vulnweb.com/

USAGE:
   argo [global options] command [command options] [arguments...]

VERSION:
   1.0

AUTHOR:
   Recar <https://github.com/Ciyfly>

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --target value, -t value       Specify the entry point for testing
   --targetsfile value, -f value  The specified target file list has each target separated by a new line, just like other tools we have used in the past
   --unheadless, --uh             Is the default interface disabled? Specify 'uh' to enable the interface (default: false)
   --trace                        Whether to display the elements of operation after opening the interface (default: false)
   --slow value                   The default delay time for operating after enabling  (default: 1000)
   --username value, -u value     If logging in, the default username  (default: "argo")
   --password value, -p value     If logging in, the default password (default: "argo123")
   --email value                  If logging in, the default email (default: "argo@recar.com")
   --phone value                  If logging in, the default phone (default: "18888888888")
   --playback value               Support replay like headless YAML scripts
   --testplayback                 If opened, then directly end after executing the specified playback script (default: false)
   --proxy value                  Set up a proxy, for example, http://127.0.0.1:3128
   --tabcount value, -c value     The maximum number of tab pages that can be opened (default: 10)
   --tabtimeout value             Set the maximum running time for the tab, and close the tab if it exceeds the limit. The unit is in seconds (default: 30)
   --browsertimeout value         Set the maximum running time for the browser, and close the browser if it exceeds the limit. The unit is in seconds (default: 18000)
   --save value                   The default name for the saved result is 'target' without a file extension. For example, to save as 'test', use the command '--save test'
   --format value                 Result output format separated by commas, multiple formats can be output at one time, and the supported formats include txt, json, xlsx, and html (default: "txt,json")
   --debug                        Do you want to output debug information? (default: false)
   --dev                          Enable dev mode. This will activate the browser interface mode and stop after accessing the page for development and debugging purposes (default: false)
   --help, -h                     show help
   --version, -v                  print the version
```

## 运行

### 测试 http://testphp.vulnweb.com/

```shell
./argo -t http://testphp.vulnweb.com/ --format txt 
```

![](imgs/demo.gif)

### 测试 DVWA 需要登录的

```shell
./argo -t http://192.168.192.128:8080/ -u admin -p password --format txt
```

![](imgs/dvwa.gif)

### 配置代理

```shell
./argo -t http://testphp.vulnweb.com/ --format txt --proxy http://127.0.0.1:3128
./argo -t http://testphp.vulnweb.com/ --format txt --proxy http://username:password@127.0.0.1:3128
```

### 使用 playback 实现dvwa的登录  

```shell
./argo -t http://192.168.192.128:8080/ --playback headless/dvwa.yml  --format txt
```

### 通过 -f 指定目标文件 即多个target

目前是按顺序单个目标的执行 永远是一个浏览器在运行
如果 有需要登录的记得增加 用户名密码参数 目前只支持单个  

```shell
cat targets.txt
http://testphp.vulnweb.com/
http://192.168.192.128:8080/

# run argo
./argo -f targets.txt  --format txt
```

### 支持控制事件触发间隔 --slow
默认是1000ms 即1s 事件如 输入 点击后会等待间隔时间后再继续触发  

```shell
./argo -t http://192.168.192.128:8080/  --slow 
```

### 查看浏览器界面 --uh

指定 --uh 参数 程序运行就会显示浏览器界面可以用调试 对应的 可以开启 trace 参数来跟着事件触发的元素  

```shell
./argo -t http://192.168.192.128:8080/  --uh
```

### debug输出

```shell
./argo -t http://192.168.192.128:8080/  --debug
```

debug输出会输出详细的泛化去重 解析url等信息 如下图  

![](imgs/debug.jpg)


### 支持多种输出格式
例如 html输出结果如下  

![](imgs/result_html.jpg)

excel表格输出结果如下  

![](imgs/result_excel.jpg)




## 说明
是w8ay师傅知识星球的作业 也是我最近工作相关的于是就做了这个程序 是基于各位大佬的基础上进行设计和实现 当然有任何问题欢迎提 issus 或者跟我联系   
目前程序还有很多地方可以完善这种程序肯定是需要时间和测试来打磨的 下一步准备测试程序去逼近自动化能完成的 以及下一步准备更好的支持web2.0的网站


## 参考

http://blog.fatezero.org/2018/04/09/web-scanner-crawler-02/  
https://pkg.go.dev/github.com/go-rod/go-rod-chinese  
https://chat.openai.com/  

## FAQ 

如果运行出现杀毒报毒 如图 说 leakless.exe 有问题 可以信任他 这是 go-rod用来控制chrome进程遗留问题的 源码在这里 https://github.com/ysmood/leakless 当然也可以自己编译替换  
![](imgs/leakless.png)

argo的编译后的程序是 github action 自动编译的 当然可以自己编译  

## 交流
可以加群交流Argo方面的问题  

![](imgs/Argo交流群.jpg)



## 声明

使用argo前请遵守当地法律,argo仅提供给教育行为使用。