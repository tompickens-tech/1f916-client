package web

import (
	"strings"
	"testing"
)

func TestIdenticon(t *testing.T) {
	svg1 := GenerateIdenticonSVG("test-handle", 24)
	svg2 := GenerateIdenticonSVG("test-handle", 24)

	if svg1 != svg2 {
		t.Errorf("Identicon generation is not deterministic")
	}

	if strings.Contains(svg1, "style=") {
		t.Errorf("Identicon SVG contains banned style= attribute: %s", svg1)
	}

	if !strings.Contains(svg1, `aria-hidden="true"`) {
		t.Errorf("Identicon SVG missing aria-hidden attribute")
	}
}
