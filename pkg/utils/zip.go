package utils

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func Uncompress(fileName string) error {
	file, err := os.Open(fileName)
	if err != nil {
		return fmt.Errorf("Uncompress open error %s : %w", fileName, err)
	}
	defer file.Close()

	extension := strings.ToLower(filepath.Ext(fileName))
	fmt.Println(extension)
	switch extension {
	case ".gz":
		err = UncompressTarGz(file)
	case ".zip":
		err = UncompressZip(file)
	default:
		return fmt.Errorf("Uncompress file is not zip or tar.gz: %s", extension)
	}

	if err != nil {
		return fmt.Errorf("Uncompress error: %w", err)
	}

	return nil
}

func UncompressTarGz(file *os.File) error {
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("gzip error: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("UncompressTarGz error: %w", err)
		}

		filePath := header.Name
		targetFile, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("UncompressTarGz create error: %w", err)
		}
		defer targetFile.Close()

		if _, err := io.Copy(targetFile, tarReader); err != nil {
			return fmt.Errorf("UncompressTarGz copy file error: %w", err)
		}

		fmt.Println("UncompressTarGz success:", filePath)
	}

	return nil
}

func UncompressZip(file *os.File) error {
	zipReader, err := zip.OpenReader(file.Name())
	if err != nil {
		return fmt.Errorf("UncompressZip error: %w", err)
	}
	defer zipReader.Close()

	for _, file := range zipReader.File {
		filePath := file.Name

		if file.FileInfo().IsDir() {
			err := os.MkdirAll(filePath, os.ModePerm)
			if err != nil {
				return fmt.Errorf("UncompressZip makedir error: %w", err)
			}
			continue
		}

		targetFile, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("UncompressZip create file error: %w", err)
		}
		defer targetFile.Close()

		srcFile, err := file.Open()
		if err != nil {
			return fmt.Errorf("UncompressZip open file error: %w", err)
		}
		defer srcFile.Close()

		if _, err := io.Copy(targetFile, srcFile); err != nil {
			return fmt.Errorf("UncompressZip copy file error: %w", err)
		}

		fmt.Println("UncompressZip success:", filePath)
	}

	return nil
}
