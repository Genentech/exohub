package serve

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const (
	sessionCookieName = "exo_session"
	sessionIdleTTL    = 8 * time.Hour
	cleanupInterval   = 15 * time.Minute
)

// Session holds per-user state for a web browse session.
type Session struct {
	ID         string
	Token      *oauth2.Token
	Username   string
	LastAccess time.Time
}

// SessionStore manages browser sessions in memory.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	done     chan struct{}
}

// NewSessionStore creates a store and starts a background cleanup goroutine.
func NewSessionStore() *SessionStore {
	s := &SessionStore{
		sessions: make(map[string]*Session),
		done:     make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// Stop terminates the cleanup goroutine.
func (s *SessionStore) Stop() {
	close(s.done)
}

// GetOrCreate returns the session for the request, creating one if needed.
// It sets the session cookie on the response — use this for regular HTTP handlers.
func (s *SessionStore) GetOrCreate(w http.ResponseWriter, r *http.Request) *Session {
	if sess := s.GetFromRequest(r); sess != nil {
		return sess
	}

	sess := s.Create()
	http.SetCookie(w, sessionCookie(sess.ID))
	return sess
}

// GetFromRequest returns the session for the request's cookie, or nil if not found.
func (s *SessionStore) GetFromRequest(r *http.Request) *Session {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	return s.Get(cookie.Value)
}

// Create allocates a new session and adds it to the store.
func (s *SessionStore) Create() *Session {
	sess := &Session{
		ID:         generateSessionID(),
		LastAccess: time.Now(),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}

// Get returns the session for the given ID, or nil if not found.
func (s *SessionStore) Get(id string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if ok {
		sess.LastAccess = time.Now()
	}
	return sess
}

// Remove deletes a session by ID.
func (s *SessionStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evictExpired()
		case <-s.done:
			return
		}
	}
}

func (s *SessionStore) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, sess := range s.sessions {
		if now.Sub(sess.LastAccess) > sessionIdleTTL {
			delete(s.sessions, id)
		}
	}
}

// sessionCookie builds the session cookie for the given ID.
func sessionCookie(id string) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
