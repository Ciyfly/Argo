package playback

import (
	"argo/pkg/conf"
	"argo/pkg/log"
	"argo/pkg/utils"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/go-rod/rod"
	"gopkg.in/yaml.v2"
)

type PlayBackScript struct {
	ID      string  `yaml:"id"`
	Info    Info    `yaml:"info"`
	Steps   []Steps `yaml:"steps"`
	Matcher Matcher `yaml:"matcher"`
}
type Info struct {
	Name   string `yaml:"name"`
	Author string `yaml:"author"`
	Tags   string `yaml:"tags"`
}
type Setp struct {
	Xpath  string `yaml:"xpath"`
	Value  string `yaml:"value,omitempty"`
	Action string `yaml:"action"`
}
type Steps struct {
	SetpList []Setp `yaml:"setp"`
}
type Matcher struct {
	Value []string `yaml:"value"`
}

func Run(scriptPath string, page *rod.Page) {
	page.WaitLoad()
	// 这里我觉得playback的作用就是帮助登录和操作业务逻辑 那么如果存在那一定是要先执行的 并且只有第一次才会进行执行
	if !utils.IsExist(scriptPath) {
		log.Logger.Errorf("headless script not exist: %s", scriptPath)
		return
	}
	scriptByte, err := ioutil.ReadFile(scriptPath)
	if err != nil {
		log.Logger.Errorf("headless script read err: %s", err)
		return
	}
	var playbackScript PlayBackScript
	err = yaml.Unmarshal(scriptByte, &playbackScript)
	if err != nil {
		log.Logger.Errorf("headless script load yaml err: %s", err)
	}
	log.Logger.Debugf("run headerless script: %s by %s", playbackScript.Info.Name, playbackScript.Info.Author)
	for _, steps := range playbackScript.Steps {
		for _, step := range steps.SetpList {
			xpath := step.Xpath
			value := step.Value
			action := step.Action
			log.Logger.Debugf("xpath: %s value: %s action: %s", xpath, value, action)
			hasElemenet, element, err := page.HasX(xpath)
			if !hasElemenet {
				log.Logger.Errorf("xpath not found element: %s", xpath)
				return
			}
			if err != nil {
				log.Logger.Errorf("headless script xpath %s match element err: %s", xpath, err)
				return
			}
			log.Logger.Debugf("e: %s", element.MustHTML())
			switch action {
			case "click":
				log.Logger.Debugf("click: %s", element.MustHTML())
				element.Eval(`()=>{this.click();}`)
			case "input":
				log.Logger.Debugf("input %s : %s", value, element.MustHTML())
				js := fmt.Sprintf("()=>{this.value='%s'}", value)
				element.Eval(js)
			}
			// 延迟一会 默认使用 auto的slow
			time.Sleep(time.Duration(conf.GlobalConfig.AutoConf.Slow) * time.Millisecond)
		}

	}
}
