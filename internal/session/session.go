package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/tompickens06-tech/1f916-client/internal/vault"
)

type Session struct {
	Mu                sync.Mutex
	ID                string
	Email             string
	Handle            string
	CitizenKeyBytes   []byte // secret key bytes 1f916_sk_...
	ReadTokenBytes    []byte
	WriteTokenBytes   []byte
	Repo              string
	IsRecovery        bool // Session opened via recovery file
	RecoveryFileBytes []byte

	// Pulse state for the unread badge in the shared layout.
	hasUnread      bool
	lastPulseCheck time.Time

	// Flash notices: written by a write handler, read once by the page that
	// renders next, and cleared only after that page rendered successfully.
	notices []string

	LastActive time.Time
}

func (s *Session) CitizenKey() string {
	if s == nil {
		return ""
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return string(s.CitizenKeyBytes)
}

// HasKey reports whether this session holds a citizen key. It is nil-safe so
// every guard in the web package can read `if !sess.HasKey()` and nothing has
// to carry the old `sess == nil || sess.CitizenKey() == ""` idiom.
func (s *Session) HasKey() bool {
	if s == nil {
		return false
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return len(s.CitizenKeyBytes) > 0
}

func (s *Session) WriteToken() string {
	if s == nil {
		return ""
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return string(s.WriteTokenBytes)
}

func (s *Session) ReadToken() string {
	if s == nil {
		return ""
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return string(s.ReadTokenBytes)
}

// BeginPulse reports whether the caller should fetch a fresh pulse, and claims
// the fetch if so. LastPulseCheck is written BEFORE the lock is released, so
// two simultaneous requests cannot both read a stale timestamp and both fetch.
func (s *Session) BeginPulse(maxAge time.Duration) bool {
	if s == nil {
		return false
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !s.lastPulseCheck.IsZero() && time.Since(s.lastPulseCheck) < maxAge {
		return false
	}
	s.lastPulseCheck = time.Now()
	return true
}

// SetUnread records the answer of the last pulse.
func (s *Session) SetUnread(unread bool) {
	if s == nil {
		return
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.hasUnread = unread
}

// HasUnread is what the badge in the shared layout renders.
func (s *Session) HasUnread() bool {
	if s == nil {
		return false
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.hasUnread
}

// LastPulseCheck is exposed for tests and diagnostics.
func (s *Session) LastPulseCheck() time.Time {
	if s == nil {
		return time.Time{}
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.lastPulseCheck
}

// SetNotices stores the flash notices from a write receipt.
func (s *Session) SetNotices(lines []string) {
	if s == nil {
		return
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.notices = append([]string(nil), lines...)
}

// PeekNotices reads the flash notices WITHOUT clearing them. The page renders
// into its buffer first; only a good buffer earns a ClearNotices.
func (s *Session) PeekNotices() []string {
	if s == nil {
		return nil
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if len(s.notices) == 0 {
		return nil
	}
	return append([]string(nil), s.notices...)
}

// ClearNotices drops the flash notices. Called only after the page they were
// rendered into was written out.
func (s *Session) ClearNotices() {
	if s == nil {
		return
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.notices = nil
}

func (s *Session) zeroLocked() {
	if len(s.CitizenKeyBytes) > 0 {
		vault.ZeroBytes(s.CitizenKeyBytes)
		s.CitizenKeyBytes = nil
	}
	if len(s.WriteTokenBytes) > 0 {
		vault.ZeroBytes(s.WriteTokenBytes)
		s.WriteTokenBytes = nil
	}
	if len(s.ReadTokenBytes) > 0 {
		vault.ZeroBytes(s.ReadTokenBytes)
		s.ReadTokenBytes = nil
	}
	if len(s.RecoveryFileBytes) > 0 {
		vault.ZeroBytes(s.RecoveryFileBytes)
		s.RecoveryFileBytes = nil
	}
	s.notices = nil
}

func (s *Session) ZeroSecrets() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.zeroLocked()
}

type Manager struct {
	mutex    sync.Mutex
	sessions map[string]*Session
	stopChan chan struct{}
}

func NewManager() *Manager {
	m := &Manager{
		sessions: make(map[string]*Session),
		stopChan: make(chan struct{}),
	}
	go m.sweeper()
	return m
}

func (m *Manager) Stop() {
	close(m.stopChan)
}

func (m *Manager) sweeper() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.SweepOnce()
		case <-m.stopChan:
			return
		}
	}
}

func (m *Manager) SweepOnce() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for id, sess := range m.sessions {
		sess.Mu.Lock()
		idle := time.Since(sess.LastActive) > 30*time.Minute
		if idle {
			sess.zeroLocked()
		}
		sess.Mu.Unlock()

		if idle {
			delete(m.sessions, id)
		}
	}
}

func GenerateRandomID(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) GetSession(id string) *Session {
	if id == "" {
		return nil
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()

	sess, ok := m.sessions[id]
	if !ok {
		return nil
	}

	sess.Mu.Lock()
	idle := time.Since(sess.LastActive) > 30*time.Minute
	if idle {
		sess.zeroLocked()
	} else {
		sess.LastActive = time.Now()
	}
	sess.Mu.Unlock()

	if idle {
		delete(m.sessions, id)
		return nil
	}

	return sess
}

func (m *Manager) SetSession(sess *Session) {
	if sess == nil || sess.ID == "" {
		return
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()

	sess.Mu.Lock()
	sess.LastActive = time.Now()
	sess.Mu.Unlock()

	m.sessions[sess.ID] = sess
}

func (m *Manager) ClearSession(id string) {
	if id == "" {
		return
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if sess, ok := m.sessions[id]; ok {
		sess.ZeroSecrets()
		delete(m.sessions, id)
	}
}

func (m *Manager) UpdateWriteToken(id string, token string) {
	if id == "" {
		return
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if sess, ok := m.sessions[id]; ok {
		sess.Mu.Lock()
		if len(sess.WriteTokenBytes) > 0 {
			vault.ZeroBytes(sess.WriteTokenBytes)
		}
		sess.WriteTokenBytes = []byte(token)
		sess.LastActive = time.Now()
		sess.Mu.Unlock()
	}
}
