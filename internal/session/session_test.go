package session

import (
	"testing"
	"time"
)

func TestSessionManager(t *testing.T) {
	mgr := NewManager()

	sess1 := &Session{
		ID:              "sess1",
		Email:           "user1@example.com",
		Handle:          "user1",
		CitizenKeyBytes: []byte("1f916_sk_testkey1"),
		WriteTokenBytes: []byte("pat_write1"),
		LastActive:      time.Now(),
	}

	mgr.SetSession(sess1)

	active := mgr.GetSession("sess1")
	if active == nil {
		t.Fatalf("Expected active session for sess1, got nil")
	}
	if active.Handle != "user1" {
		t.Errorf("Active handle = %s, want user1", active.Handle)
	}

	// Test idle timeout (30 minutes)
	sess1.LastActive = time.Now().Add(-31 * time.Minute)
	if mgr.GetSession("sess1") != nil {
		t.Errorf("Expected session to be locked after 30 mins idle, but got non-nil")
	}
	if len(sess1.CitizenKeyBytes) != 0 {
		t.Errorf("Expected idle expired session CitizenKeyBytes to be zeroed")
	}

	// Test separate session
	sess2 := &Session{
		ID:              "sess2",
		Email:           "user2@example.com",
		Handle:          "user2",
		CitizenKeyBytes: []byte("1f916_sk_testkey2"),
		WriteTokenBytes: []byte("pat_write2"),
		LastActive:      time.Now(),
	}

	mgr.SetSession(sess2)
	active2 := mgr.GetSession("sess2")
	if active2 == nil || active2.Handle != "user2" {
		t.Errorf("Expected session 2 to be retrieved")
	}

	// Test logout / clear
	mgr.ClearSession("sess2")
	if mgr.GetSession("sess2") != nil {
		t.Errorf("Expected nil session after clear, got active session")
	}
	if len(sess2.CitizenKeyBytes) != 0 {
		t.Errorf("Expected cleared session secret key to be zeroed")
	}
}
