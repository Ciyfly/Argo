package inject

import (
	"argo/pkg/utils"
	"embed"
	"fmt"

	"github.com/go-rod/rod"
	"github.com/projectdiscovery/gologger"
)

//go:embed after/*.js
var afterFs embed.FS

//go:embed before/*.js
var befoerFs embed.FS

var AfterScriptMap map[string]string
var BeforeScriptMap map[string]string

func LoadScript() {
	AfterScriptMap = make(map[string]string)
	BeforeScriptMap = make(map[string]string)
	afterFileInfos, err := afterFs.ReadDir("after")
	if err != nil {
		gologger.Error().Msgf("load after script err %s", err)
		return
	}
	beforeFileInfos, err := befoerFs.ReadDir("before")
	if err != nil {
		gologger.Error().Msgf("load before script err %s", err)
		return
	}

	for _, fileInfo := range beforeFileInfos {
		content, err := befoerFs.ReadFile(fmt.Sprintf("before/%s", fileInfo.Name()))
		if err != nil {
			gologger.Error().Msgf("inject before script: %s err: %s", fileInfo.Name(), err)
		} else {
			name := utils.GetNameByPath(fileInfo.Name())
			BeforeScriptMap[name] = string(content)
		}
	}
	for _, fileInfo := range afterFileInfos {
		content, err := afterFs.ReadFile(fmt.Sprintf("after/%s", fileInfo.Name()))
		if err != nil {
			gologger.Error().Msgf("inject after script: %s err: %s", fileInfo.Name(), err)
		} else {
			name := utils.GetNameByPath(fileInfo.Name())
			AfterScriptMap[name] = string(content)
		}
	}

	if len(afterFileInfos) == 0 && len(beforeFileInfos) == 0 {
		gologger.Warning().Msg("没有找到注入js脚本")
	}

}

func InjectScript(page *rod.Page, stage int) {
	// dom 加载之前
	if stage == 0 {
		for name, content := range BeforeScriptMap {
			_, err := page.Eval(content)
			if err != nil {
				gologger.Error().Msgf("inject before script %s err: %s", name, err)
			}
		}
	} else {
		// dom 之后的
		for name, content := range AfterScriptMap {
			_, err := page.Eval(content)
			if err != nil {
				gologger.Error().Msgf("inject after script %s err: %s", name, err)
			}
		}
	}
}
