package f916

import (
	"testing"
)

func TestAuditModerationChain(t *testing.T) {
	h1 := "hash_001"
	h2 := "hash_002"
	h3 := "hash_003"

	events := []ModerationEvent{
		{ID: 1, Kind: "reg", CreatedAt: 100, Hash: &h1},
		{ID: 2, Kind: "ban", CreatedAt: 200, PrevHash: &h1, Hash: &h2},
		{ID: 3, Kind: "pin", CreatedAt: 300, PrevHash: &h2, Hash: &h3},
	}

	res := AuditModerationChain(events)
	if !res.Valid {
		t.Errorf("Expected valid chain audit, got invalid: %s", res.ErrorMessage)
	}

	// Tamper with chain
	badPrev := "hash_corrupted"
	events[2].PrevHash = &badPrev

	badRes := AuditModerationChain(events)
	if badRes.Valid {
		t.Errorf("Expected invalid chain audit for corrupted prev_hash, got valid")
	}
	if badRes.BrokenAtEvent != 3 {
		t.Errorf("Expected broken at event 3, got %d", badRes.BrokenAtEvent)
	}
}
