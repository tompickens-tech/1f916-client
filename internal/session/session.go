package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/tompickens06-tech/1f916-client/internal/vault"
)

type Session struct {
	ID         string
	Email      string
	Handle     string
	CitizenKey string // secret key 1f916_sk_...
	ReadToken  string
	WriteToken string
	Repo       string
	CSRFToken  string
	IsRecovery bool // Session opened via recovery file (no store/token write capabilities)

	LastActive time.Time
}

func (s *Session) ZeroSecrets() {
	if s.CitizenKey != "" {
		keyBytes := []byte(s.CitizenKey)
		vault.ZeroBytes(keyBytes)
		s.CitizenKey = ""
	}
	if s.WriteToken != "" {
		tokenBytes := []byte(s.WriteToken)
		vault.ZeroBytes(tokenBytes)
		s.WriteToken = ""
	}
}

type Manager struct {
	mutex   sync.RWMutex
	current *Session
}

func NewManager() *Manager {
	return &Manager{}
}

func GenerateRandomID(bytesLen int) string {
	b := make([]byte, bytesLen)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) GetActiveSession() *Session {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.current == nil {
		return nil
	}

	// 30-minute idle lock
	if time.Since(m.current.LastActive) > 30*time.Minute {
		// Idle lock triggered! Secret key & write token zeroed.
		return nil
	}

	m.current.LastActive = time.Now()
	return m.current
}

func (m *Manager) SetSession(sess *Session) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// One unlocked identity per container: zero old identity first
	if m.current != nil {
		m.current.ZeroSecrets()
	}

	sess.LastActive = time.Now()
	m.current = sess
}

func (m *Manager) ClearSession() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.current != nil {
		m.current.ZeroSecrets()
		m.current = nil
	}
}

func (m *Manager) UpdateWriteToken(token string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.current != nil {
		m.current.WriteToken = token
		m.current.LastActive = time.Now()
	}
}
