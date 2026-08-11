package session

import (
	"sync"
	"testing"
)

func TestSessionConcurrency(t *testing.T) {
	mgr := NewManager()
	
	sess := &Session{
		ID:         "test_session_id",
		CitizenKeyBytes: []byte("initial_secret"),
	}
	mgr.SetSession(sess)

	s := mgr.GetSession("test_session_id")
	if s == nil {
		t.Fatal("session not found")
	}

	var wg sync.WaitGroup
	
	// Reader 1: simulate a request reading CitizenKey
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = s.CitizenKey()
		}
	}()
	
	// Writer 1: simulate a key rotation
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			s.Mu.Lock()
			s.CitizenKeyBytes = []byte("new_secret")
			s.Mu.Unlock()
		}
	}()

	// Reader 2: simulate another request reading CitizenKey
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = s.CitizenKey()
		}
	}()

	wg.Wait()
}
