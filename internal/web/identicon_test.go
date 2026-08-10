package web

import (
	"testing"
)

func TestIdenticon(t *testing.T) {
	ic1 := BuildIdenticon("test-handle")
	ic2 := BuildIdenticon("test-handle")

	if ic1.Fill != ic2.Fill {
		t.Errorf("Identicon fill color is not deterministic")
	}

	if len(ic1.Cells) != len(ic2.Cells) {
		t.Errorf("Identicon cells count is not deterministic")
	}

	for i := range ic1.Cells {
		if ic1.Cells[i] != ic2.Cells[i] {
			t.Errorf("Identicon cell %d mismatch: %+v vs %+v", i, ic1.Cells[i], ic2.Cells[i])
		}
	}

	if ic1.Fill == "" {
		t.Errorf("Identicon fill color is empty")
	}
}
