package static

import (
	"argo/pkg/req"
	"bufio"
	"fmt"
	"net/http"
	"strings"
)

// 验证 robots
func robotsSpider(URL string) (navigationRequests []string) {
	URL = strings.TrimSuffix(URL, "/")
	requestURL := fmt.Sprintf("%s/robots.txt", URL)
	response := req.GetResponseWithBody(requestURL)
	if response == nil {
		return nil
	}
	defer response.Body.Close()
	navigationRequests = append(navigationRequests, parseRobotsReader(response)...)
	return navigationRequests
}

func parseRobotsReader(resp *http.Response) (navigationRequests []string) {
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		text := scanner.Text()
		splitted := strings.SplitN(text, ": ", 2)
		if len(splitted) < 2 {
			continue
		}
		directive := strings.ToLower(splitted[0])
		if strings.HasPrefix(directive, "allow") || strings.EqualFold(directive, "disallow") {
			target := req.AbsoluteURL(strings.Trim(splitted[1], " "), resp.Request.URL.Scheme)
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
