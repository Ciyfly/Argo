package inject

import "argo/pkg/conf"

// FormRulesCheck 供引擎启动时校验表单填充规则正则（详细错误信息）。
func FormRulesCheck() (string, error) {
	return formRulesJSON(conf.GlobalConfig.FormConf)
}
