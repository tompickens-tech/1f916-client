package store

import (
	"testing"
)

func TestNormalizeRepo(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"owner/repo", "owner/repo"},
		{"https://github.com/owner/repo", "owner/repo"},
		{"github.com/owner/repo", "owner/repo"},
		{" /owner/repo/ ", "owner/repo"},
	}

	for _, tt := range tests {
		got := NormalizeRepo(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeRepo(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
