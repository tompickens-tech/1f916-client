package session

import (
	"testing"
	"time"
)

func TestSessionManager(t *testing.T) {
	mgr := NewManager()

	sess1 := &Session{
		ID:         "sess1",
		Email:      "user1@example.com",
		Handle:     "user1",
		CitizenKey: "1f916_sk_testkey1",
		WriteToken: "pat_write1",
		LastActive: time.Now(),
	}

	mgr.SetSession(sess1)

	active := mgr.GetActiveSession()
	if active == nil {
		t.Fatalf("Expected active session, got nil")
	}
	if active.Handle != "user1" {
		t.Errorf("Active handle = %s, want user1", active.Handle)
	}

	// Test idle timeout (30 minutes)
	sess1.LastActive = time.Now().Add(-31 * time.Minute)
	if mgr.GetActiveSession() != nil {
		t.Errorf("Expected session to be locked after 30 mins idle, but got non-nil")
	}

	// Test identity replacement (zeroing previous identity)
	sess2 := &Session{
		ID:         "sess2",
		Email:      "user2@example.com",
		Handle:     "user2",
		CitizenKey: "1f916_sk_testkey2",
		WriteToken: "pat_write2",
		LastActive: time.Now(),
	}

	mgr.SetSession(sess2)
	if sess1.CitizenKey != "" {
		t.Errorf("Expected previous session secret key to be zeroed on set, got %s", sess1.CitizenKey)
	}

	// Test logout / clear
	mgr.ClearSession()
	if mgr.GetActiveSession() != nil {
		t.Errorf("Expected nil session after clear, got active session")
	}
	if sess2.CitizenKey != "" {
		t.Errorf("Expected cleared session secret key to be zeroed, got %s", sess2.CitizenKey)
	}
}
