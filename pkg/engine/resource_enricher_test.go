package engine

import "testing"

func TestParseJSResource(t *testing.T) {
	base := "https://example.com/app/main.js"
	js := []byte(`fetch('/api/users'); axios.get("https://example.com/static/data.json"); router.push('/dashboard'); const img="https://cdn.example.com/img.png";`)
	urls := parseJSResource(base, js)
	expect := map[string]bool{
		"https://example.com/api/users":        false,
		"https://example.com/static/data.json": false,
		"https://example.com/dashboard":        false,
		"https://cdn.example.com/img.png":      false,
	}
	for _, u := range urls {
		if _, ok := expect[u]; ok {
			expect[u] = true
		}
	}
	for target, seen := range expect {
		if !seen {
			t.Fatalf("missing url %s", target)
		}
	}
}

func TestParseGenericResource(t *testing.T) {
	base := "https://example.com/assets/data.json"
	body := []byte(`{"link":"https://example.com/api"}\nMore https://cdn.example.com/file`)
	urls := parseGenericResource(base, body)
	expect := map[string]bool{
		"https://example.com/api":      false,
		"https://cdn.example.com/file": false,
	}
	for _, u := range urls {
		if _, ok := expect[u]; ok {
			expect[u] = true
		}
	}
	for target, seen := range expect {
		if !seen {
			t.Fatalf("missing url %s", target)
		}
	}
}
