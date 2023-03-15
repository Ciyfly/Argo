package utils

import "encoding/base64"

func EncodeBase64(input []byte) string {
	return base64.StdEncoding.EncodeToString(input)
}
