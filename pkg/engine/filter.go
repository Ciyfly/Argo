package engine

import (
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

var StaticSuffix = []string{
	".3gp", ".aac", ".ai", ".aiff", ".apk", ".asf",
	".asx", ".au", ".avi", ".bin", ".bmp", ".bz2",
	".cab", ".crt", ".css", ".csv", ".dat", ".dll",
	".dmg", ".doc", ".docx", ".drw", ".dxf", ".eps",
	".exe", ".exif", ".flv", ".gif", ".gz", ".gz2",
	".ico", ".iso", ".jpeg", ".jpg", ".less", ".m4a",
	".m4v", ".map", ".mid", ".mng", ".mov", ".mp3",
	".mp4", ".mpeg", ".mpg", ".odg", ".odp", ".ods",
	".odt", ".ogg", ".otf", ".pct", ".pdf", ".png",
	".ppt", ".pptx", ".ps", ".psp", ".pst", ".qt",
	".ra", ".rar", ".rm", ".rmvb", ".rss", ".svg",
	".swf", ".tar", ".tif", ".tiff", ".ttf", ".txt",
	".wav", ".webp", ".wma", ".wmv", ".woff", ".woff2",
	".wps", ".xls", ".xlsx", ".xsl", ".zip",
}

func InitFilter() {
	sort.Strings(StaticSuffix)
}

func filterStatic(target string) bool {
	// fix https://static.sj.qq.com/wupload/xy/yyb_official_website/ocgyts2d.png&#34;,&#34;alias&#34;:&#34;1671069624000&#34;,&#34;report_info&#34;:{&#34;cardid&#34;:&#34;YYB_HOME_GAME_DETAIL_RELATED_BLOG&#34;,&#34;slot&#34;:1}}],&#34;cardid&#34;:&#34;YYB_HOME_GAME_DETAIL_RELATED_BLOG
	//&#34;},&#34;errors&#34;:[],&#34;report_info&#34;:{&#34;rel_exp_ids&#34;:&#34;&#34;,&#34;pos&#34;:5
	if strings.Contains(target, "&") {
		target = strings.Split(target, "&")[0]
	}
	u, err := url.Parse(target)
	if err != nil {
		return true
	}
	suffix := filepath.Ext(u.Scheme + "://" + u.Host + "/" + u.Path)
	index := sort.SearchStrings(StaticSuffix, suffix)
	if index < len(StaticSuffix) && StaticSuffix[index] == suffix {
		return true
	}
	return false
}
