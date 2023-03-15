package utils

import (
	"crypto/md5"
	"fmt"
)

func GetMD5(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h)
}
