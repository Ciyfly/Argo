# Argo

<div align=center><img width="250" height="250" src="imgs/logo.jpg"/></div>

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
9. 支持url深度层数控制
10. 支持控制是否存储完整请求响应base64字符串 json格式
11. 支持程序自动升级 
12. 支持 指定 远程浏览器 本地浏览器
13. 更新了 增加多个目标爬取后指定生成到一个文件 增加指定UA

注意: 我开放了很多参数 几个参数配合使用可以达到自己想要的效果

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
   v1.0

AUTHOR:
   Recar <https://github.com/Ciyfly>

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --help, -h     show help
   --version, -v  print the version

   Browser

   --slow value  The default delay time for operating after enabling  (default: 1000)
   --trace       Display operation elements after interface opens? (default: false)

   Config

   --browsertimeout value      Set max browser run time, close if limit exceeded. Unit is seconds. (default: 900)
   --chrome value              Specify the Chrome executable path, e.g. --chrome /opt/google/chrome/chrome
   --maxdepth value            Scrape web content with increasing depth by crawling URLs, stop at max depth. (default: 5)
   --remote value              Specify remote Chrome address, e.g. --remote http://127.0.0.1:3000
   --tabcount value, -c value  The maximum number of tab pages that can be opened (default: 10)
   --tabtimeout value          Set max tab run time, close if limit exceeded. Unit is seconds. (default: 15)

   Data

   --email value               Default email if logging in. (default: "argo@recar.com")
   --password value, -p value  Default password if logging in. (default: "argo123")
   --phone value               Default phone if logging in. (default: "18888888888")
   --username value, -u value  Default username if logging in. (default: "argo")

   Debug

   --debug             Output debug info? (default: false)
   --dev               Enable dev mode, activates browser interface and stops after page access for dev purposes. (default: false)
   --testplayback      irectly end if open, after specified playback script execution. (default: false)
   --unheadless, --uh  Default interface disabled? Use 'uh' to enable it. (default: false)

   OutPut

   --format value     Output format separated by commas, txt, json, xlsx, html supported. (default: "txt,json")
   --outputdir value  save output to directory
   --quiet            Enable quiet mode to output only the URL information that has been retrieved, in JSON format (default: false)
   --save value       Result saved as 'target' by default. Use '--save test' to save as 'test'.

   Update

   --update  update self (default: false)

   Use

   --norrs                        No storage of req-res strings, saves memory, suitable for large scans. (default: false)
   --playback value               Support replay like headless YAML scripts
   --proxy value                  Set up a proxy, for example, http://127.0.0.1:3128
   --target value, -t value       Specify the entry point for testing
   --targetsfile value, -f value  The file list has targets separated by new lines, like other tools we've used before.



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
### 多个目标结果存到一个文件里
```
# 指定格式的合并输出
./argo -f targets.txt --mergedOutput results.txt    # 只输出txt格式
./argo -f targets.txt --mergedOutput results.json   # 只输出json格式

# 多格式输出
./argo -f targets.txt --mergedOutput results --format txt,json,xlsx  # 输出多个格式文件
## 这种方式 如果文件名没变 多次执行 都会追加到一个文件里

```

### 指定UA
```
argo  -t http://testphp.vulnweb.com/  --userAgent recar123
```

### 指定浏览器

加了两个参数 一个是指定本地下载好的浏览器 一个是指定远程浏览器 

远程浏览器可以使用 https://github.com/browserless/chrome  
然后 运行 容器 监听端口 argo配置即可  
```
# 指定本地浏览器路径
./argo -t http://192.168.192.128:8080/ --chrome chrome_path

# 指定远程浏览器ip 端口
./argo -t http://192.168.192.128:8080/ --remote http://127.0.0.1:3000
```


### 设置浏览器超时时间 页面超时时间

浏览器默认超时时间 900s 

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

### 控制不存储 请求响应的base64字符串
存储的话会消耗内存 降低性能  
```
./argo -t http://192.168.192.128:8080/  --norrs
```

### url深度层数控制

默认是3 超过最大深度就会抛弃这个url  
```
./argo -t http://192.168.192.128:8080/   --maxdepth 层数
```

### 程序升级

升级会去github判断版本对比 自动下载新版本 根据平台自动判断 下载较慢的话可以选择手动下载的方式  
```
./argo -t http://192.168.192.128:8080/  --update
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

如果第一次运行报错 error while loading shared libraries: libatk-1.0.so.0: cannot open shared object file: No such file or directory  
解决如下
```shell
# centos
yum install pango.x86_64 libXcomposite.x86_64 libXcursor.x86_64 libXdamage.x86_64 libXext.x86_64 libXi.x86_64 libXtst.x86_64 cups-libs.x86_64 libXScrnSaver.x86_64 libXrandr.x86_64 GConf2.x86_64 alsa-lib.x86_64 atk.x86_64 gtk3.x86_64 -y

# ubuntu
apt-get install -yq --no-install-recommends libasound2 libatk1.0-0 libc6 libcairo2 libcups2 libdbus-1-3 libexpat1 libfontconfig1 libgcc1 libgconf-2-4 libgdk-pixbuf2.0-0 libglib2.0-0 libgtk-3-0 libnspr4 libpango-1.0-0 libpangocairo-1.0-0 libstdc++6 libx11-6 libx11-xcb1 libxcb1 libxcursor1 libxdamage1 libxext6 libxfixes3 libxi6 libxrandr2 libxrender1 libxss1 libxtst6 libnss3 libgbm-dev
```

## 交流
可以加群交流Argo方面的问题  

![](imgs/Argo交流群.jpg)

欢迎关注公众号
![](https://user-images.githubusercontent.com/16779256/262313682-b324004a-5b4a-483e-9fc0-145b6706955e.png)



## 声明

使用argo前请遵守当地法律,argo仅提供给教育行为使用。
