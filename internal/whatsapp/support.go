package whatsapp

import (
	"sync"
	"time"
)

// SupportSession represents an active customer support ticket session.
type SupportSession struct {
	PhoneNumber  string
	CustomerJID  string
	PushName     string
	StartedAt    time.Time
	LastActive   time.Time
	LastNotified time.Time
}

// SupportSessionManager manages ongoing live support chats in memory.
type SupportSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*SupportSession
	timeout  time.Duration
}

// NewSupportSessionManager creates a new SupportSessionManager.
func NewSupportSessionManager(timeout time.Duration) *SupportSessionManager {
	if timeout <= 0 {
		timeout = 1 * time.Hour
	}
	mgr := &SupportSessionManager{
		sessions: make(map[string]*SupportSession),
		timeout:  timeout,
	}

	go mgr.cleanupLoop()
	return mgr
}

// OpenSession creates or resets a live support session for the customer.
func (m *SupportSessionManager) OpenSession(phone, jid, name string) *SupportSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	s := &SupportSession{
		PhoneNumber:  phone,
		CustomerJID:  jid,
		PushName:     name,
		StartedAt:    now,
		LastActive:   now,
		LastNotified: time.Time{},
	}
	m.sessions[phone] = s
	return s
}

// GetSession retrieves an active session if it exists and hasn't timed out.
func (m *SupportSessionManager) GetSession(phone string) *SupportSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, exists := m.sessions[phone]
	if !exists {
		return nil
	}

	if time.Since(s.LastActive) > m.timeout {
		delete(m.sessions, phone)
		return nil
	}

	s.LastActive = time.Now()
	return s
}

// ShouldSendFeedback checks if the bot should send the "Pesan kamu telah diteruskan..." confirmation to the customer.
// It returns true on the first message or if it has been at least 3 minutes since the last notification, avoiding duplicate spam.
func (m *SupportSessionManager) ShouldSendFeedback(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, exists := m.sessions[phone]
	if !exists {
		return false
	}

	now := time.Now()
	if s.LastNotified.IsZero() || now.Sub(s.LastNotified) > 3*time.Minute {
		s.LastNotified = now
		return true
	}

	return false
}

// HasActiveSession checks if a customer is currently in an active support session.
func (m *SupportSessionManager) HasActiveSession(phone string) bool {
	return m.GetSession(phone) != nil
}

// CloseSession ends an ongoing support session.
func (m *SupportSessionManager) CloseSession(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[phone]; exists {
		delete(m.sessions, phone)
		return true
	}
	return false
}

func (m *SupportSessionManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for phone, session := range m.sessions {
			if now.Sub(session.LastActive) > m.timeout {
				delete(m.sessions, phone)
			}
		}
		m.mu.Unlock()
	}
}
