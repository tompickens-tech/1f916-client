package web

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Palette-locked colors (presentation attributes only, no style="")
var identiconPalette = []string{
	"#2563eb", // blue
	"#7c3aed", // violet
	"#db2777", // pink
	"#ea580c", // orange
	"#16a34a", // green
	"#0284c7", // sky
	"#4f46e5", // indigo
	"#0d9488", // teal
}

// GenerateIdenticonSVG returns inline SVG string for a citizen handle.
// Size is passed as width/height in px.
func GenerateIdenticonSVG(handle string, size int) string {
	h := sha256.Sum256([]byte(strings.ToLower(handle)))

	colorIdx := int(h[0]) % len(identiconPalette)
	fillColor := identiconPalette[colorIdx]

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg width="%d" height="%d" viewBox="0 0 5 5" aria-hidden="true">`, size, size)
	fmt.Fprintf(&sb, `<rect width="5" height="5" fill="#f1f5f9"/>`)

	// 5x5 symmetric grid (columns 0, 1, 2 determine columns 3, 4)
	byteIdx := 1
	for row := 0; row < 5; row++ {
		for col := 0; col < 3; col++ {
			b := h[byteIdx%len(h)]
			byteIdx++
			if b%2 == 0 {
				fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="1" height="1" fill="%s"/>`, col, row, fillColor)
				if col < 2 {
					symCol := 4 - col
					fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="1" height="1" fill="%s"/>`, symCol, row, fillColor)
				}
			}
		}
	}
	sb.WriteString(`</svg>`)

	return sb.String()
}
