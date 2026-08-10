package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/tompickens06-tech/1f916-client/internal/vault"
)

type Session struct {
	ID                string
	Email             string
	Handle            string
	CitizenKeyBytes   []byte // secret key bytes 1f916_sk_...
	ReadToken         string
	WriteTokenBytes   []byte
	Repo              string
	CSRFToken         string
	IsRecovery        bool // Session opened via recovery file
	RecoveryFileBytes []byte

	LastActive time.Time
}

func (s *Session) CitizenKey() string {
	return string(s.CitizenKeyBytes)
}

func (s *Session) WriteToken() string {
	return string(s.WriteTokenBytes)
}

func (s *Session) ZeroSecrets() {
	if len(s.CitizenKeyBytes) > 0 {
		vault.ZeroBytes(s.CitizenKeyBytes)
		s.CitizenKeyBytes = nil
	}
	if len(s.WriteTokenBytes) > 0 {
		vault.ZeroBytes(s.WriteTokenBytes)
		s.WriteTokenBytes = nil
	}
	if len(s.RecoveryFileBytes) > 0 {
		vault.ZeroBytes(s.RecoveryFileBytes)
		s.RecoveryFileBytes = nil
	}
}

type Manager struct {
	mutex    sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
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

	// 30-minute idle lock
	if time.Since(sess.LastActive) > 30*time.Minute {
		sess.ZeroSecrets()
		delete(m.sessions, id)
		return nil
	}

	sess.LastActive = time.Now()
	return sess
}

func (m *Manager) SetSession(sess *Session) {
	if sess == nil || sess.ID == "" {
		return
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()

	sess.LastActive = time.Now()
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
		if len(sess.WriteTokenBytes) > 0 {
			vault.ZeroBytes(sess.WriteTokenBytes)
		}
		sess.WriteTokenBytes = []byte(token)
		sess.LastActive = time.Now()
	}
}
