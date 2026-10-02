package static

import (
	"argo/pkg/log"
	"argo/pkg/req"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

func sitemapXmlSpider(URL string) (navigationRequests []string) {
	URL = strings.TrimSuffix(URL, "/")
	requestURL := fmt.Sprintf("%s/sitemap.xml", URL)
	response := req.GetResponseWithBody(requestURL)
	if response == nil {
		return nil
	}
	defer response.Body.Close()
	navigationRequests = append(navigationRequests, parseSiteMapXmlReader(response)...)
	return navigationRequests
}

type sitemapStruct struct {
	URLs    []parsedURL `xml:"url"`
	Sitemap []parsedURL `xml:"sitemap"`
}

type parsedURL struct {
	Loc string `xml:"loc"`
}

func parseSiteMapXmlReader(resp *http.Response) (navigationRequests []string) {
	sitemap := sitemapStruct{}
	if err := xml.NewDecoder(resp.Body).Decode(&sitemap); err != nil {
		log.Logger.Warnf("sitemap could not decode xml %s", err)
		return nil
	}
	for _, url := range sitemap.URLs {
		target := req.AbsoluteURL(strings.Trim(url.Loc, " \t\n"), resp.Request.URL.Scheme)
		navigationRequests = append(navigationRequests, target)
	}
	for _, url := range sitemap.Sitemap {
		target := req.AbsoluteURL(strings.Trim(url.Loc, " \t\n"), resp.Request.URL.Scheme)
		navigationRequests = append(navigationRequests, target)
	}
	return
}
