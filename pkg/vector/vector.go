package vector

import (
	"math"
	"strings"

	"github.com/kljensen/snowball"
	"golang.org/x/net/html"
)

type Vector map[string]int

func extractWords(htmlStr string) []string {
	dom := html.NewTokenizer(strings.NewReader(htmlStr))

	words := []string{}

	for {
		tt := dom.Next()
		if tt == html.ErrorToken {
			break
		}

		if tt == html.TextToken {
			t := dom.Token()
			data := strings.TrimSpace(t.Data)
			if len(data) > 0 {
				stemmed, err := snowball.Stem(data, "english", true)
				if err != nil {
					continue
				}
				words = append(words, stemmed)
			}
		}
	}

	return words
}

func HTMLToVector(html string) Vector {
	words := extractWords(html)
	vector := make(Vector)

	for _, word := range words {
		vector[word]++
	}
	return vector
}

func CosineSimilarity(v1, v2 Vector) float64 {
	var sum1, sum2, sum3 float64

	for k, v := range v1 {
		sum1 += float64(v * v)
		if v2v, ok := v2[k]; ok {
			sum3 += float64(v * v2v)
		}
	}
	for _, v := range v2 {
		sum2 += float64(v * v)
	}
	if sum1 == 0 || sum2 == 0 { // 判断分母是否为0
		return 0
	}
	result := sum3 / (sqrt(sum1) * sqrt(sum2))
	if math.IsNaN(result) { // 判断结果是否为NaN
		return 0
	}
	return result
}

func sqrt(n float64) float64 {
	return float64(math.Sqrt(n))
}
