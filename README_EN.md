# Argo

<div align=center><img width="250" height="250" src="imgs/logo.jpg"/></div>

[中文](./README.md) | English

A general-purpose automated crawler based on go-rod for automatically obtaining website URLs, implemented using headless browser technology.

## Features
Supports the following:
1. Intelligent triggering of page events - prioritizes processing of new DOM elements after clicks
2. Intelligent website login (currently does not support scenarios with CAPTCHAs)
3. Full traffic hook support - captures all browser traffic (requests and responses) through go-rod's HijackRequests
4. URL deduplication - final stored output contains only unique URLs
5. Multiple output formats supported - txt, json, xlsx, html
6. Supports playback of yaml format scripts - executes operations in sequence
7. Supports browser interface display and debug output
8. Proxy support
9. URL depth control
10. Option to store complete request-response base64 strings in JSON format
11. Automatic program upgrade support
12. Support for specifying remote or local browser
13. Updated to support multiple target crawling with single file output and custom UA specification

Note: I've exposed many parameters that can be combined to achieve desired effects

## Installation

You can download the latest version directly from https://github.com/Ciyfly/Argo/releases
No need to manually download Chrome - running the program will automatically download Chrome

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

## Usage

### Testing http://testphp.vulnweb.com/

```shell
./argo -t http://testphp.vulnweb.com/ --format txt 
```

![](imgs/demo.gif)

### Testing DVWA (requires login)

```shell
./argo -t http://192.168.192.128:8080/ -u admin -p password --format txt
```

![](imgs/dvwa.gif)

### Configuring Proxy

```shell
./argo -t http://testphp.vulnweb.com/ --format txt --proxy http://127.0.0.1:3128
./argo -t http://testphp.vulnweb.com/ --format txt --proxy http://username:password@127.0.0.1:3128
```

### Using playback for DVWA login

```shell
./argo -t http://192.168.192.128:8080/ --playback headless/dvwa.yml  --format txt
```

### Using -f to specify target file (multiple targets)

Currently executes targets sequentially with one browser running at a time.
Remember to add username/password parameters if login is required (currently supports single credential set)

```shell
cat targets.txt
http://testphp.vulnweb.com/
http://192.168.192.128:8080/

# run argo
./argo -f targets.txt  --format txt
```

### Merging multiple target results into one file
```shell
# Specify format for merged output
./argo -f targets.txt --mergedOutput results.txt    # txt format only
./argo -f targets.txt --mergedOutput results.json   # json format only

# Multiple format output
./argo -f targets.txt --mergedOutput results --format txt,json,xlsx  # outputs multiple format files
## Note: Results will be appended to existing files if the filename remains the same across multiple executions
```

### Specifying User Agent
```shell
argo  -t http://testphp.vulnweb.com/  --userAgent recar123
```

### Specifying Browser

Two parameters added: one for specifying local downloaded browser, another for remote browser

Remote browser can use https://github.com/browserless/chrome
Run container, listen to port, and configure argo accordingly

```shell
# Specify local browser path
./argo -t http://192.168.192.128:8080/ --chrome chrome_path

# Specify remote browser IP and port
./argo -t http://192.168.192.128:8080/ --remote http://127.0.0.1:3000
```

### Setting Browser and Page Timeout

Default browser timeout is 900s

### Control Event Trigger Interval --slow
Default is 1000ms (1s). Events like input and clicks will wait for the interval before triggering again

```shell
./argo -t http://192.168.192.128:8080/  --slow 
```

### View Browser Interface --uh

Use --uh parameter to display browser interface for debugging. Can enable trace parameter to follow event-triggered elements

```shell
./argo -t http://192.168.192.128:8080/  --uh
```

### Control Request-Response Base64 String Storage
Storing consumes memory and reduces performance
```shell
./argo -t http://192.168.192.128:8080/  --norrs
```

### URL Depth Control

Default is 3, URLs beyond maximum depth are discarded
```shell
./argo -t http://192.168.192.128:8080/   --maxdepth depth_number
```

### Program Upgrade

Compares version with GitHub and automatically downloads new version based on platform
```shell
./argo -t http://192.168.192.128:8080/  --update
```

### Debug Output

```shell
./argo -t http://192.168.192.128:8080/  --debug
```

Debug output shows detailed generalization, deduplication, URL parsing, etc.

![](imgs/debug.jpg)

### Multiple Output Format Support
HTML output example:

![](imgs/result_html.jpg)

Excel output example:

![](imgs/result_excel.jpg)

## Description
This is an assignment from w8ay's knowledge planet and relates to my recent work. It was designed and implemented based on various experts' foundations. Any issues are welcome through issues or direct contact.
The program still has many areas for improvement, and such programs require time and testing to refine. The next step is to test the program to approach automation capabilities and better support Web 2.0 websites.

## References

http://blog.fatezero.org/2018/04/09/web-scanner-crawler-02/  
https://pkg.go.dev/github.com/go-rod/go-rod-chinese  
https://chat.openai.com/  

## FAQ 

If antivirus reports issues with leakless.exe, you can trust it - it's used by go-rod to control Chrome process residual issues. Source code is at https://github.com/ysmood/leakless, or you can compile and replace it yourself.
![](imgs/leakless.png)

Argo's compiled program is automatically built by GitHub action, but you can compile it yourself.

If first run errors with "error while loading shared libraries: libatk-1.0.so.0: cannot open shared object file: No such file or directory"
Solution:
```shell
# centos
yum install pango.x86_64 libXcomposite.x86_64 libXcursor.x86_64 libXdamage.x86_64 libXext.x86_64 libXi.x86_64 libXtst.x86_64 cups-libs.x86_64 libXScrnSaver.x86_64 libXrandr.x86_64 GConf2.x86_64 alsa-lib.x86_64 atk.x86_64 gtk3.x86_64 -y

# ubuntu
apt-get install -yq --no-install-recommends libasound2 libatk1.0-0 libc6 libcairo2 libcups2 libdbus-1-3 libexpat1 libfontconfig1 libgcc1 libgconf-2-4 libgdk-pixbuf2.0-0 libglib2.0-0 libgtk-3-0 libnspr4 libpango-1.0-0 libpangocairo-1.0-0 libstdc++6 libx11-6 libx11-xcb1 libxcb1 libxcursor1 libxdamage1 libxext6 libxfixes3 libxi6 libxrandr2 libxrender1 libxss1 libxtst6 libnss3 libgbm-dev
```

## Communication
Join the group to discuss Argo-related issues

![](imgs/Argo交流群.jpg)

Welcome to follow the official account
![](https://user-images.githubusercontent.com/16779256/262313682-b324004a-5b4a-483e-9fc0-145b6706955e.png)

## Disclaimer

Please comply with local laws when using Argo. Argo is provided for educational purposes only.