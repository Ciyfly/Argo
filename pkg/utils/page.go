package utils

import (
	"errors"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func GetCurrentUrlByPage(page *rod.Page) (string, error) {
	if page == nil {
		return "", errors.New("page nil")
	}
	pageInfo, err := page.Info()
	if err != nil {
		return "", err
	}
	return pageInfo.URL, nil
}

func GetPageInfoByPage(page *rod.Page) (*proto.TargetTargetInfo, error) {
	if page == nil {
		return nil, errors.New("page nil")
	}
	pageInfo, err := page.Info()
	if err != nil {
		return nil, err
	}
	return pageInfo, nil
}
