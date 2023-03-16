/*
 * @Author: Recar
 * @Date: 2023-03-16 21:13:52
 * @LastEditors: Recar
 * @LastEditTime: 2023-03-16 21:14:30
 */
package utils

import (
	"net/http"
	"time"
)

func CheckTarget(target string) bool {
	client := http.Client{
		Timeout: time.Second * 5,
	}
	resp, err := client.Get(target)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true
	}
	return false
}
