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

func getSuffix(target string) (string, error) {
	if strings.Contains(target, "&") {
		target = strings.Split(target, "&")[0]
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	return filepath.Ext(u.Scheme + "://" + u.Host + "/" + u.Path), nil
}

func isStaticSuffix(suffix string) bool {
	index := sort.SearchStrings(StaticSuffix, suffix)
	return index < len(StaticSuffix) && StaticSuffix[index] == suffix
}

func filterStaticPendUrl(target string) bool {
	suffix, err := getSuffix(target)
	if err != nil {
		return true
	}

	if suffix == ".js" {
		return true
	}

	return isStaticSuffix(suffix)
}

func filterStatic(target string) bool {
	suffix, err := getSuffix(target)
	if err != nil {
		return true
	}

	return isStaticSuffix(suffix)
}
