package f916

import (
	"encoding/json"
	"os"
	"testing"
)

func TestParseCapturedTestData(t *testing.T) {
	// 1. Test front.json
	frontData, err := os.ReadFile("testdata/front.json")
	if err != nil {
		t.Fatalf("Failed to read front.json: %v", err)
	}
	posts, err := parseFeedPosts(frontData)
	if err != nil {
		t.Fatalf("Failed to parse front.json: %v", err)
	}
	if len(posts) == 0 {
		t.Errorf("Expected non-empty posts from front.json")
	}

	// 2. Test post_325.json
	postData, err := os.ReadFile("testdata/post_325.json")
	if err != nil {
		t.Fatalf("Failed to read post_325.json: %v", err)
	}
	var detail PostDetail
	if err := json.Unmarshal(postData, &detail); err != nil {
		t.Fatalf("Failed to unmarshal post_325.json: %v", err)
	}
	if detail.Post.ID != 325 {
		t.Errorf("Expected post ID 325, got %d", detail.Post.ID)
	}
	if len(detail.Comments) == 0 {
		t.Errorf("Expected non-empty comments in post 325")
	}

	// 3. Test citizens.json
	citData, err := os.ReadFile("testdata/citizens.json")
	if err != nil {
		t.Fatalf("Failed to read citizens.json: %v", err)
	}
	var citList CitizenList
	if err := json.Unmarshal(citData, &citList); err != nil {
		t.Fatalf("Failed to unmarshal citizens.json: %v", err)
	}
	if len(citList.Citizens) == 0 {
		t.Errorf("Expected non-empty citizens list")
	}
}

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com", "https://example.com"},
		{"http://test.org/path", "http://test.org/path"},
		{"javascript:alert(1)", ""},
		{"ftp://files.org", ""},
		{"invalid url", ""},
	}
	for _, tt := range tests {
		got := SanitizeURL(tt.input)
		if got != tt.want {
			t.Errorf("SanitizeURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
