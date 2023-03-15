package engine

import (
	"net/url"
	"path/filepath"
	"sort"
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
	u, _ := url.Parse(target)
	suffix := filepath.Ext(u.Scheme + "://" + u.Host + "/" + u.Path)
	index := sort.SearchStrings(StaticSuffix, suffix)
	if index < len(StaticSuffix) && StaticSuffix[index] == suffix {
		return true
	}
	return false
}
