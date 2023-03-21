package files

import (
	"argo/pkg/utils"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/projectdiscovery/retryablehttp-go"
	errorutil "github.com/projectdiscovery/utils/errors"
)

type sitemapXmlCrawler struct {
	httpclient *retryablehttp.Client
}

// Visit visits the provided URL with file crawlers
func (r *sitemapXmlCrawler) Visit(URL string) (navigationRequests []string, err error) {
	URL = strings.TrimSuffix(URL, "/")
	requestURL := fmt.Sprintf("%s/sitemap.xml", URL)
	req, err := retryablehttp.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, errorutil.NewWithTag("sitemapcrawler", "could not create request").Wrap(err)
	}
	req.Header.Set("User-Agent", utils.WebUserAgent())

	resp, err := r.httpclient.Do(req)
	if err != nil {
		return nil, errorutil.NewWithTag("sitemapcrawler", "could not do request").Wrap(err)
	}
	defer resp.Body.Close()

	navigationRequests, err = r.parseReader(resp.Body, resp)
	if err != nil {
		return nil, errorutil.NewWithTag("sitemapcrawler", "could not parse sitemap").Wrap(err)
	}
	return
}

type sitemapStruct struct {
	URLs    []parsedURL `xml:"url"`
	Sitemap []parsedURL `xml:"sitemap"`
}

type parsedURL struct {
	Loc string `xml:"loc"`
}

func (r *sitemapXmlCrawler) parseReader(reader io.Reader, resp *http.Response) (navigationRequests []string, err error) {
	sitemap := sitemapStruct{}
	if err := xml.NewDecoder(reader).Decode(&sitemap); err != nil {
		return nil, errorutil.NewWithTag("sitemapcrawler", "could not decode xml").Wrap(err)
	}
	for _, url := range sitemap.URLs {
		target := utils.AbsoluteURL(strings.Trim(url.Loc, " \t\n"), resp.Request.URL.Scheme)
		navigationRequests = append(navigationRequests, target)
	}
	for _, url := range sitemap.Sitemap {

		target := utils.AbsoluteURL(strings.Trim(url.Loc, " \t\n"), resp.Request.URL.Scheme)

		navigationRequests = append(navigationRequests, target)
	}
	return
}
