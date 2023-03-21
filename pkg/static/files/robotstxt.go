package files

import (
	"argo/pkg/utils"
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/projectdiscovery/retryablehttp-go"
	errorutil "github.com/projectdiscovery/utils/errors"
)

type robotsTxtCrawler struct {
	httpclient *retryablehttp.Client
}

// Visit visits the provided URL with file crawlers
func (r *robotsTxtCrawler) Visit(URL string) ([]string, error) {
	URL = strings.TrimSuffix(URL, "/")
	requestURL := fmt.Sprintf("%s/robots.txt", URL)
	req, err := retryablehttp.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, errorutil.NewWithTag("robotscrawler", "could not create request").Wrap(err)
	}
	req.Header.Set("User-Agent", utils.WebUserAgent())

	resp, err := r.httpclient.Do(req)
	if err != nil {
		return nil, errorutil.NewWithTag("robotscrawler", "could not do request").Wrap(err)
	}
	defer resp.Body.Close()

	return r.parseReader(resp.Body, resp)
}

func (r *robotsTxtCrawler) parseReader(reader io.Reader, resp *http.Response) (navigationRequests []string, err error) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		text := scanner.Text()
		splitted := strings.SplitN(text, ": ", 2)
		if len(splitted) < 2 {
			continue
		}
		directive := strings.ToLower(splitted[0])
		if strings.HasPrefix(directive, "allow") || strings.EqualFold(directive, "disallow") {

			target := utils.AbsoluteURL(strings.Trim(splitted[1], " "), resp.Request.URL.Scheme)

			URL := strings.TrimSuffix(strings.Split(resp.Request.URL.String(), "/robots.txt")[0], "/")

			if strings.HasPrefix(target, "/") {
				target = URL + target
			} else {
				target = URL + "/" + target
			}
			navigationRequests = append(navigationRequests, target)
		}
	}
	return
}
