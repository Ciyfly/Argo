package inject

import "argo/pkg/conf"

// confSpiderEnabled 蜘蛛叠加层开关（默认开，仅在有头模式下注入才有意义）。
func confSpiderEnabled() bool { return conf.GlobalConfig.AutoConf.Spider }
