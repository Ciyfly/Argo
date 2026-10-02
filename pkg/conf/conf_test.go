package conf

import (
	"argo/pkg/log"
	"flag"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

// argoFlags 覆盖 MergeArgs 会读取的所有 flag，与 cmd/argo.go 中的定义保持一致。
func argoFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "target", Aliases: []string{"t"}},
		&cli.StringFlag{Name: "targetsfile", Aliases: []string{"f"}},
		&cli.BoolFlag{Name: "unheadless", Aliases: []string{"uh"}},
		&cli.BoolFlag{Name: "trace"},
		&cli.Float64Flag{Name: "slow", Value: 1000},
		&cli.StringFlag{Name: "userAgent"},
		&cli.StringFlag{Name: "username", Aliases: []string{"u"}, Value: "argo"},
		&cli.StringFlag{Name: "password", Aliases: []string{"p"}, Value: "argo123"},
		&cli.StringFlag{Name: "email", Value: "argo@recar.com"},
		&cli.StringFlag{Name: "phone", Value: "18888888888"},
		&cli.StringFlag{Name: "playback"},
		&cli.BoolFlag{Name: "testplayback"},
		&cli.StringFlag{Name: "proxy"},
		&cli.IntFlag{Name: "tabcount", Aliases: []string{"c"}, Value: 10},
		&cli.IntFlag{Name: "tabtimeout", Value: 15},
		&cli.IntFlag{Name: "browsertimeout", Value: 600},
		&cli.StringFlag{Name: "chrome"},
		&cli.StringFlag{Name: "remote"},
		&cli.StringFlag{Name: "save"},
		&cli.StringFlag{Name: "outputdir"},
		&cli.StringFlag{Name: "mergedOutput"},
		&cli.BoolFlag{Name: "quiet"},
		&cli.StringFlag{Name: "format", Value: "txt,json"},
		&cli.BoolFlag{Name: "debug"},
		&cli.BoolFlag{Name: "dev"},
		&cli.BoolFlag{Name: "norrs"},
		&cli.IntFlag{Name: "maxdepth", Value: 5},
		&cli.BoolFlag{Name: "update"},
		// pprof 调试服务
		&cli.BoolFlag{Name: "pprof", Value: false},
		&cli.StringFlag{Name: "pprofaddr", Value: "127.0.0.1:5208"},
	}
}

func newTestContext(t *testing.T, args []string) *cli.Context {
	t.Helper()
	// 日志在 main 里初始化，测试里需要自己初始化，否则 Logger 为 nil 会 panic
	log.Init(false, false)
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	for _, f := range argoFlags() {
		if err := f.Apply(set); err != nil {
			t.Fatalf("apply flag err: %s", err)
		}
	}
	if err := set.Parse(args); err != nil {
		t.Fatalf("parse args err: %s", err)
	}
	return cli.NewContext(nil, set, nil)
}

// 回归测试：自动生成的默认配置文件必须能被正确解析。
// 历史上这份配置用的是 tabcount/tabtimeout/maxdepth（少了下划线），
// 与结构体上的 yaml 标签（tab_count/tab_timeout/max_depth）不匹配，
// 这几项在生成的配置文件里永远读不到。
func TestDefaultYamlConfigKeysMatchStructTags(t *testing.T) {
	var parsed Conf
	if err := yaml.Unmarshal([]byte(defaultYamlConfigStr), &parsed); err != nil {
		t.Fatalf("默认配置无法解析: %s", err)
	}

	// 这几个值故意写成与结构体默认值不同的数字，才能验证是否真的被解析进来
	if parsed.BrowserConf.TabCount != 10 {
		t.Errorf("tab_count 未被解析，期望 10，实际 %d（键名可能写成了 tabcount）", parsed.BrowserConf.TabCount)
	}
	if parsed.BrowserConf.TabTimeout != 15 {
		t.Errorf("tab_timeout 未被解析，期望 15，实际 %d（键名可能写成了 tabtimeout）", parsed.BrowserConf.TabTimeout)
	}
	if parsed.BrowserConf.BrowserTimeout != 600 {
		t.Errorf("browser_timeout 未被解析，期望 600，实际 %d（键名可能写成了 browsertimeout）", parsed.BrowserConf.BrowserTimeout)
	}
	if parsed.BrowserConf.MaxDepth != 3 {
		t.Errorf("max_depth 未被解析，期望 3，实际 %d（键名可能写成了 maxdepth）", parsed.BrowserConf.MaxDepth)
	}
	if parsed.LoginConf.Username != "argo" {
		t.Errorf("login.username 未被解析，实际 %q", parsed.LoginConf.Username)
	}
	if len(parsed.AutoConf.Filter) == 0 {
		t.Errorf("auto.filter 未被解析")
	}
}

// 回归测试：仓库里自带的 configs/config.yml 也必须能被正确解析，
// 避免默认配置和随仓库分发的配置两边漂移。
func TestShippedConfigFileParses(t *testing.T) {
	// 测试运行目录是 pkg/conf，配置在仓库根的 configs/ 下
	candidates := []string{
		filepath.Join("..", "..", "configs", "config.yml"),
		filepath.Join("configs", "config.yml"),
	}
	var data []byte
	var used string
	for _, c := range candidates {
		b, err := ioutil.ReadFile(c)
		if err == nil {
			data, used = b, c
			break
		}
	}
	if data == nil {
		t.Skipf("未找到 configs/config.yml（从 %s 运行），跳过", mustGetwd(t))
	}

	var parsed Conf
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("%s 解析失败: %s", used, err)
	}
	// 自带配置里 tab_count 写的是 10，max_depth 写的是 10
	if parsed.BrowserConf.TabCount == 0 {
		t.Errorf("%s 的 tab_count 未被解析", used)
	}
	if parsed.BrowserConf.MaxDepth == 0 {
		t.Errorf("%s 的 max_depth 未被解析", used)
	}
	if !strings.Contains(string(data), "命令行参数的默认值会覆盖") {
		t.Errorf("%s 缺少配置优先级说明文案", used)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return d
}

// 回归测试：pprof 调试服务必须默认关闭。
// 历史上它是启动即监听 0.0.0.0:5208 且无法关闭，会把运行时内存和调用栈暴露出去。
func TestPprofDisabledByDefault(t *testing.T) {
	GlobalConfig = &Conf{}
	ctx := newTestContext(t, []string{"-t", "http://example.com/"})
	MergeArgs(ctx)

	if GlobalConfig.Pprof {
		t.Errorf("未传 --pprof 时 Pprof 应为 false（默认关闭）")
	}
	// 默认地址必须是本机回环，不能是 0.0.0.0
	if GlobalConfig.PprofAddr != "127.0.0.1:5208" {
		t.Errorf("pprof 默认地址应为本机回环，实际 %q", GlobalConfig.PprofAddr)
	}
	if strings.HasPrefix(GlobalConfig.PprofAddr, "0.0.0.0") {
		t.Errorf("pprof 默认地址不应监听 0.0.0.0，实际 %q", GlobalConfig.PprofAddr)
	}
}

// 回归测试：--pprof 与 --pprofaddr 必须真正生效。
func TestPprofFlagsTakeEffect(t *testing.T) {
	GlobalConfig = &Conf{}
	ctx := newTestContext(t, []string{"-t", "http://example.com/", "--pprof"})
	MergeArgs(ctx)
	if !GlobalConfig.Pprof {
		t.Errorf("传了 --pprof 但 Pprof 仍为 false")
	}

	GlobalConfig = &Conf{}
	ctx = newTestContext(t, []string{"-t", "http://example.com/", "--pprof", "--pprofaddr", "127.0.0.1:5309"})
	MergeArgs(ctx)
	if GlobalConfig.PprofAddr != "127.0.0.1:5309" {
		t.Errorf("--pprofaddr 未生效，实际 %q", GlobalConfig.PprofAddr)
	}
}

// 回归测试：MergeArgs 必须真正读到 --trace 的值。
// 历史上这里读的是 c.Bool("entrace")（名字拼错），导致 --trace 永远为 false。
func TestMergeArgsReadsTraceFlag(t *testing.T) {
	GlobalConfig = &Conf{}
	ctx := newTestContext(t, []string{"-t", "http://example.com/"})
	MergeArgs(ctx)
	if GlobalConfig.BrowserConf.Trace {
		t.Errorf("未传 --trace 时 Trace 应为 false")
	}

	GlobalConfig = &Conf{}
	ctx = newTestContext(t, []string{"-t", "http://example.com/", "--trace"})
	MergeArgs(ctx)
	if !GlobalConfig.BrowserConf.Trace {
		t.Errorf("传了 --trace 但 Trace 仍为 false，说明读的不是 'trace' 这个 flag 名")
	}
}

// 记录当前行为：命令行默认值会覆盖配置文件里的值。
// 这是已知设计（README 里有说明），用测试固化下来，避免以后误以为配置文件能生效。
func TestMergeArgsCLIDefaultOverridesConfigFile(t *testing.T) {
	GlobalConfig = &Conf{}
	GlobalConfig.BrowserConf.Proxy = "http://127.0.0.1:8080"
	GlobalConfig.BrowserConf.TabTimeout = 30

	ctx := newTestContext(t, []string{"-t", "http://example.com/"})
	MergeArgs(ctx)

	if GlobalConfig.BrowserConf.Proxy != "" {
		t.Errorf("按当前设计，配置文件里的 proxy 会被命令行默认值清空，实际: %q", GlobalConfig.BrowserConf.Proxy)
	}
	if GlobalConfig.BrowserConf.TabTimeout != 15 {
		t.Errorf("按当前设计，配置文件里的 tab_timeout 会被命令行默认值改回 15，实际: %d", GlobalConfig.BrowserConf.TabTimeout)
	}

	// 显式传参时，命令行值生效
	GlobalConfig = &Conf{}
	GlobalConfig.BrowserConf.TabTimeout = 30
	ctx = newTestContext(t, []string{"-t", "http://example.com/", "--tabtimeout", "42"})
	MergeArgs(ctx)
	if GlobalConfig.BrowserConf.TabTimeout != 42 {
		t.Errorf("显式传 --tabtimeout 42 应生效，实际: %d", GlobalConfig.BrowserConf.TabTimeout)
	}
}
