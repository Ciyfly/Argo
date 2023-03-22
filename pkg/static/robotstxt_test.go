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

func TestRobotsTxtParseReader(t *testing.T) {
	requests := []string{}

	content := `User-agent: *
Disallow: /test/misc/known-files/robots.txt.found

User-agent: *
Disallow: /test/includes/

# User-agent: Googlebot
# Allow: /random/

Sitemap: https://example.com/sitemap.xml`
	parsed, _ := url.Parse("http://localhost/robots.txt")
	body := ioutil.NopCloser(bytes.NewReader([]byte(content)))
	navigationRequests := parseRobotsReader(&http.Response{Request: &http.Request{URL: parsed}, Body: body})
	for _, navReq := range navigationRequests {
		fmt.Println(navReq)
		requests = append(requests, navReq)
	}
	require.ElementsMatch(t, requests, []string{
		"http://localhost/test/includes/",
		"http://localhost/test/misc/known-files/robots.txt.found",
	}, "could not get correct elements")
}
