/*
 * @Author: Recar
 * @Date: 2023-04-14 21:10:37
 * @LastEditors: Recar
 * @LastEditTime: 2023-04-15 13:57:55
 */
package updateself

import (
	"argo/pkg/utils"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/cheggaaa/pb/v3"
)

type LastVersionInfo struct {
	TagName string   `json:"tag_name"`
	Assets  []Assets `json:"assets"`
	Body    string   `json:"body"`
}
type Assets struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func getLastVersion() (LastVersionInfo, error) {
	lvi := LastVersionInfo{}
	url := "https://api.github.com/repos/CiyFly/Argo/releases/latest"
	client := &http.Client{}
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("req error: %s\n", err)
		return lvi, err
	}
	resp, err := client.Do(request)
	if err != nil {
		fmt.Printf("req error: %s\n", err)
		return lvi, err
	}
	defer resp.Body.Close()
	body, readErr := ioutil.ReadAll(resp.Body)
	if readErr != nil {
		fmt.Printf("req read error: %s\n", readErr)
		return lvi, readErr
	}
	jsonErr := json.Unmarshal(body, &lvi)
	if jsonErr != nil {
		fmt.Printf("parser github api json error: %s\n", readErr)
		return lvi, jsonErr
	}
	return lvi, nil
}

func downloadLastVersion(lastVersion, url, name string) error {
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("download last version: %s err: %s\n", lastVersion, err)
		return err
	}
	defer resp.Body.Close()

	file, err := os.Create(name)
	if err != nil {
		fmt.Printf("download last version: %s err: %s\n", lastVersion, err)
		return err
	}
	defer file.Close()

	size, _ := strconv.Atoi(resp.Header.Get("Content-Length"))
	chunkSize := size / 10     // 将文件分成 10 个块
	bar := pb.Full.Start(size) // 在进度条库中使用 Full 模式

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		start := i * chunkSize
		end := (i + 1) * chunkSize
		if i == 9 { // 最后一个块可能会有大小不一致的问题，所以需要单独处理
			end = size
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()

			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				fmt.Printf("download last version: %s err: %s\n", lastVersion, err)
				return
			}
			req.Header.Set("Range", fmt.Sprintf("bytes=%v-%v", start, end-1))

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				fmt.Printf("download last version: %s err: %s\n", lastVersion, err)
				return
			}
			defer resp.Body.Close()

			buf := make([]byte, 1024*1024) // 调整缓冲区大小，提高下载速度
			var downloaded int64
			for {
				n, err := resp.Body.Read(buf)
				if err != nil && err != io.EOF {
					fmt.Printf("download last version: %s err: %s\n", lastVersion, err)
					return
				}
				if n > 0 {
					_, err = file.WriteAt(buf[:n], downloaded+int64(start))
					if err != nil {
						fmt.Printf("download last version: %s err: %s\n", lastVersion, err)
						return
					}
					downloaded += int64(n)
					bar.Add(n)
				}
				if downloaded == int64((end - start)) {
					break
				}
			}
		}(start, end)
	}

	wg.Wait()
	bar.Finish()
	fmt.Println("Download complete.")
	return nil
}

func get_assest_name() string {
	// Argo-v1.2.13-linux-amd64.tar.gz
	var os, arrch string
	if runtime.GOOS == "darwin" {
		os = "macOS"
	} else {
		os = runtime.GOOS
	}
	if runtime.GOARCH == "arm" {
		arrch = "arm64"
	} else {
		arrch = runtime.GOARCH
	}
	return os + "-" + arrch
}

func CheckIfUpgradeRequired(version string) {
	lvi, err := getLastVersion()

	if err != nil {
		return
	}
	if version < lvi.TagName {
		fmt.Printf("need update current: %s last: %s\n", version, lvi.TagName)
		fmt.Println("last version info")
		fmt.Println("------------------------------")
		fmt.Print(lvi.Body)
		fmt.Println("\n------------------------------")
		var downloadUrl, name string
		goOsArrch := get_assest_name()
		for _, assest := range lvi.Assets {
			if strings.Contains(assest.Name, goOsArrch) {
				downloadUrl = assest.BrowserDownloadURL
				name = assest.Name
				break
			}
		}
		fmt.Printf("download url: %s\n", downloadUrl)
		err := downloadLastVersion(lvi.TagName, downloadUrl, name)
		if err != nil {
			return
		}
		executablePath, err := os.Executable()
		if err != nil {
			fmt.Printf("get executablePath err: %s \n", err)
			return
		}
		fmt.Println("Rename old version")
		err = os.Rename(executablePath, fmt.Sprintf("old_argo_%s.bak", version))
		if err != nil {
			fmt.Printf("mv old version err: %s \n", err)
			return
		}
		fmt.Println("decompress new version")
		errUncompress := utils.Uncompress(name)
		if errUncompress != nil {
			fmt.Println(err)
		}
		fmt.Println("remove download file")
		err = os.Remove(name)
		if err != nil {
			fmt.Printf("del download err: %s \n", err)
		}
	} else {
		fmt.Printf("The current version is already the latest")
	}
}
