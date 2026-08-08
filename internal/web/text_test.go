package web

import (
	"testing"

	"github.com/tompickens06-tech/1f916-client/internal/f916"
)

func TestProcessBodySegments(t *testing.T) {
	text := "Check out https://1f916.ai and http://example.com/test for details. Ignore javascript:alert(1)."
	segments := ProcessBodySegments(text)

	var linkCount int
	for _, seg := range segments {
		if seg.IsLink {
			linkCount++
			if seg.Href != "https://1f916.ai" && seg.Href != "http://example.com/test" {
				t.Errorf("Unexpected link Href: %s", seg.Href)
			}
		}
	}

	if linkCount != 2 {
		t.Errorf("Expected 2 valid links, got %d", linkCount)
	}
}

func TestBuildCommentTreeCycleProtection(t *testing.T) {
	id1, id2 := int64(1), int64(2)
	comments := []f916.Comment{
		{ID: 1, ParentID: &id2, Body: "Comment 1"},
		{ID: 2, ParentID: &id1, Body: "Comment 2 (Cycle with 1)"},
	}

	// Should not hang in infinite recursion
	tree := BuildCommentTree(comments, nil)
	_ = tree
}

func TestBuildCommentTreeDropUnresolvableParent(t *testing.T) {
	id999 := int64(999)
	comments := []f916.Comment{
		{ID: 1, ParentID: nil, Body: "Root comment"},
		{ID: 2, ParentID: &id999, Body: "Orphan comment"},
	}

	tree := BuildCommentTree(comments, nil)
	if len(tree) != 1 {
		t.Errorf("Expected 1 root comment (orphan dropped), got %d", len(tree))
	}
}
