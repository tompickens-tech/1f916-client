package web

import (
	"crypto/sha256"
	"strings"
)

// Fixed palette of 8 distinct hues at locked saturation/lightness
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

type IdenticonCell struct {
	X int
	Y int
}

type Identicon struct {
	Fill  string
	Cells []IdenticonCell
}

// BuildIdenticon builds typed geometry for a citizen handle identicon.
// No markup is returned as a string per the 10 Aug 2026 standing rules update.
func BuildIdenticon(handle string) Identicon {
	h := sha256.Sum256([]byte(strings.ToLower(handle)))
	ic := Identicon{Fill: identiconPalette[int(h[0])%len(identiconPalette)]}
	b := 1
	for row := 0; row < 5; row++ {
		for col := 0; col < 3; col++ {
			if h[b%len(h)]%2 == 0 {
				ic.Cells = append(ic.Cells, IdenticonCell{col, row})
				if col < 2 {
					ic.Cells = append(ic.Cells, IdenticonCell{4 - col, row})
				}
			}
			b++
		}
	}
	return ic
}
