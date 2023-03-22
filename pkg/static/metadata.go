package static

// robots.txt sitemap.xml
func MetaDataSpider(target string) (urls []string) {
	robotsList := robotsSpider(target)
	if robotsList != nil {
		urls = append(urls, robotsList...)
	}
	sitemapList := sitemapXmlSpider(target)
	if sitemapList != nil {
		urls = append(urls, sitemapList...)
	}
	return urls
}
