package static

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSitemapXmlParseReader(t *testing.T) {
	requests := []string{}

	content := `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<sitemap>
  	<loc>
		http://security-crawl-maze.app/test/misc/known-files/sitemap.xml.found
	</loc>
	<lastmod>2019-06-19T12:00:00+00:00</lastmod>
</sitemap>
</sitemapindex>`
	parsed, _ := url.Parse("http://security-crawl-maze.app/sitemap.xml")
	body := ioutil.NopCloser(bytes.NewReader([]byte(content)))
	navigationRequests := parseSiteMapXmlReader(&http.Response{Request: &http.Request{URL: parsed}, Body: body})
	for _, navReq := range navigationRequests {
		fmt.Println(navReq)
		requests = append(requests, navReq)
	}

	require.ElementsMatch(t, requests, []string{
		"http://security-crawl-maze.app/test/misc/known-files/sitemap.xml.found",
	}, "could not get correct elements")
}
