package updateself

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

type LastVersionInfo struct {
	TagName string   `json:"tag_name"`
	Assets  []Assets `json:"assets"`
}
type Assets struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func getLastVersion() (LastVersionInfo, error) {
	lvi := LastVersionInfo{}
	url := "https://api.github.com/repos/CiyFly/Argo/releases/latest"
	// 创建HTTP客户端
	client := &http.Client{}

	// 创建请求
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println(fmt.Sprintf("req error: %s\n", err))
		return lvi, err
	}
	resp, err := client.Do(request)
	if err != nil {
		fmt.Println(fmt.Sprintf("req error: %s\n", err))
		return lvi, err
	}
	defer resp.Body.Close()
	body, readErr := ioutil.ReadAll(resp.Body)
	if readErr != nil {
		fmt.Println(fmt.Sprintf("req read error: %s\n", readErr))
		return lvi, readErr
	}
	jsonErr := json.Unmarshal(body, &lvi)
	if jsonErr != nil {
		fmt.Println(fmt.Sprintf("parser github api json error: %s\n", readErr))
		return lvi, jsonErr
	}
	return lvi, nil
}

func CheckIfUpgradeRequired(version string) bool {
	// 比较传入的版本与当前需要的最低版本
	lvi, err := getLastVersion()
	if err != nil {
		return false
	}
	fmt.Println(lvi.Assets[0].BrowserDownloadURL)
	versionFloat := strings.Replace(version, "v", "", -1)
	if version < lvi.TagName {
		fmt.Println(fmt.Sprintf("need update current: %s last: %s", version, lvi.TagName))
		return true
	}
	return false
}
