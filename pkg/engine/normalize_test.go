package engine

import "testing"

func TestNormalizePathNumbers(t *testing.T) {
	url1 := "http://example.com/user/123/orders/456"
	url2 := "http://example.com/user/999/orders/888"
	if normalizeation(url1, "GET") != normalizeation(url2, "get") {
		t.Fatalf("numeric segments should normalize to same value")
	}
}

func TestNormalizeQueryOrder(t *testing.T) {
	url1 := "https://example.com/search?q=test&limit=10"
	url2 := "HTTPS://EXAMPLE.com/search?limit=10&q=test"
	if normalizeation(url1, "GET") != normalizeation(url2, "GET") {
		t.Fatalf("query order should not affect normalization")
	}
}

func TestNormalizeDifferentMethods(t *testing.T) {
	url := "http://example.com/submit"
	if normalizeation(url, "GET") == normalizeation(url, "POST") {
		t.Fatalf("methods should impact normalization")
	}
}

func TestNormalizeClassifyUUID(t *testing.T) {
	url1 := "http://example.com/resource/123e4567-e89b-12d3-a456-426614174000"
	url2 := "http://example.com/resource/123e4567-e89b-12d3-a456-426614174999"
	if normalizeation(url1, "GET") != normalizeation(url2, "GET") {
		t.Fatalf("uuid segments should normalize")
	}
}
