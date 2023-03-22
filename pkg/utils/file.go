package utils

import (
	"argo/pkg/log"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// 判断所给路径文件/文件夹是否存在
func IsExist(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		if os.IsNotExist(err) {
			return false
		}
		log.Logger.Errorf("utils file IsExist err: %s", err)
		return false
	}
	return true
}

func GetCurrentDirectory() string {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Logger.Errorf("GetCurrentDirectory: %s", err)
	}
	return strings.Replace(dir, "\\", "/", -1)
}

func GetAllDirectoryPaths(dirPath string) []string {
	result := []string{}
	fis, err := ioutil.ReadDir(dirPath)
	if err != nil {
		log.Logger.Errorf("GetAllDirectoryPaths: %s", err)
		return result
	}
	for _, fi := range fis {
		fullname := dirPath + "/" + fi.Name()
		if fi.IsDir() {
			temp := GetAllDirectoryPaths(fullname)
			result = append(result, temp...)
		} else {
			result = append(result, fullname)
		}
	}
	return result
}

func GetNameByPath(filepath string) string {
	filenameWithSuffix := path.Base(filepath)
	fileSuffix := path.Ext(filenameWithSuffix)
	return strings.TrimSuffix(filenameWithSuffix, fileSuffix)
}

func FilterFileSuffix(filePath, suffix string) bool {
	filesuffix := path.Ext(filePath)
	if filesuffix == suffix || strings.Contains(filesuffix, suffix) {
		return true
	}
	return false
}
