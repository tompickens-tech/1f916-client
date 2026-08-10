package web

import (
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "time/tzdata" // Import time/tzdata because distroless static image carries no zoneinfo

	"github.com/tompickens06-tech/1f916-client/internal/f916"
)

// Banned template.HTML note:
// Standing Rule: Never produce template.HTML from board data for any reason.
// All rendering uses html/template's built-in escaping over plain string values and struct slices.

var urlRegex = regexp.MustCompile(`https?://[^\s<>"'\(\)]+`)

type TextSegment struct {
	Text   string
	IsLink bool
	Href   string
	Host   string
}

func ProcessBodySegments(body string) []TextSegment {
	var segments []TextSegment
	matches := urlRegex.FindAllStringIndex(body, -1)
	if len(matches) == 0 {
		return []TextSegment{{Text: body, IsLink: false}}
	}

	lastIdx := 0
	for _, match := range matches {
		start, end := match[0], match[1]

		if start > lastIdx {
			segments = append(segments, TextSegment{
				Text:   body[lastIdx:start],
				IsLink: false,
			})
		}

		rawURL := body[start:end]
		sanitized := f916.SanitizeURL(rawURL)
		if sanitized != "" {
			u, err := url.Parse(sanitized)
			host := ""
			if err == nil {
				host = u.Hostname()
			}
			segments = append(segments, TextSegment{
				Text:   rawURL,
				IsLink: true,
				Href:   sanitized,
				Host:   host,
			})
		} else {
			segments = append(segments, TextSegment{
				Text:   rawURL,
				IsLink: false,
			})
		}
		lastIdx = end
	}

	if lastIdx < len(body) {
		segments = append(segments, TextSegment{
			Text:   body[lastIdx:],
			IsLink: false,
		})
	}

	return segments
}

func FormatUTCAndRelative(epochMs int64) (string, string) {
	t := time.UnixMilli(epochMs).UTC()
	utcStr := t.Format("2006-01-02 15:04:05 UTC")

	now := time.Now().UTC()
	diff := now.Sub(t)

	var relStr string
	if diff < 0 {
		relStr = "just now"
	} else if diff < time.Minute {
		relStr = "less than a min ago"
	} else if diff < time.Hour {
		mins := int(diff.Minutes())
		relStr = fmtPlural(mins, "min") + " ago"
	} else if diff < 24*time.Hour {
		hours := int(diff.Hours())
		relStr = fmtPlural(hours, "hour") + " ago"
	} else {
		days := int(diff.Hours() / 24)
		relStr = fmtPlural(days, "day") + " ago"
	}

	return utcStr, relStr
}

func fmtPlural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strings.TrimRight(unit, "s") + "s"
}

type CommentNode struct {
	Comment   f916.Comment
	Segments  []TextSegment
	Identicon Identicon
	UTCTime   string
	RelTime   string
	Children  []*CommentNode
	Depth     int // Visual depth capped at 3
}

func BuildCommentTree(comments []f916.Comment, rootCommentID *int64) []*CommentNode {
	if len(comments) == 0 {
		return nil
	}

	// Cap at 1000 comments per standing rule
	if len(comments) > 1000 {
		comments = comments[:1000]
	}

	byID := make(map[int64]*CommentNode, len(comments))
	var nodes []*CommentNode

	for _, c := range comments {
		utcTime, relTime := FormatUTCAndRelative(c.CreatedAt)
		node := &CommentNode{
			Comment:   c,
			Segments:  ProcessBodySegments(c.Body),
			Identicon: BuildIdenticon(c.Author),
			UTCTime:   utcTime,
			RelTime:   relTime,
			Children:  make([]*CommentNode, 0),
		}
		byID[c.ID] = node
		nodes = append(nodes, node)
	}

	var rootNodes []*CommentNode
	visited := make(map[int64]bool)

	// If rootCommentID is provided (thread view), find target comment
	if rootCommentID != nil {
		target, ok := byID[*rootCommentID]
		if ok {
			target.Depth = 0
			rootNodes = append(rootNodes, target)
			buildChildren(target, byID, visited, 0)
			return rootNodes
		}
	}

	// Otherwise attach to parent or root
	for _, node := range nodes {
		if node.Comment.ParentID == nil {
			node.Depth = 0
			rootNodes = append(rootNodes, node)
			buildChildren(node, byID, visited, 0)
		} else {
			// Check if parent exists
			parent, ok := byID[*node.Comment.ParentID]
			if !ok {
				// Parent doesn't resolve in array -> drop comment per standing rule
				continue
			}
			// Will be attached via buildChildren
			_ = parent
		}
	}

	return rootNodes
}

func buildChildren(parent *CommentNode, byID map[int64]*CommentNode, visited map[int64]bool, currentDepth int) {
	if visited[parent.Comment.ID] {
		// Cycle detected in parent_id! Stop recursion immediately to avoid hanging.
		return
	}
	visited[parent.Comment.ID] = true

	for _, node := range byID {
		if node.Comment.ParentID != nil && *node.Comment.ParentID == parent.Comment.ID {
			node.Depth = currentDepth + 1
			parent.Children = append(parent.Children, node)
			buildChildren(node, byID, visited, currentDepth+1)
		}
	}
}
