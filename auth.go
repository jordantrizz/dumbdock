package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Session lifetimes for web-auth mode.
const (
	sessionCookieName = "dumbdock_session"
	sessionDuration   = 8 * time.Hour
	rememberDuration  = 30 * 24 * time.Hour
	sessionMaxBody    = 1 << 20 // 1 MiB cap on login request bodies
)

// sessionStore is an in-memory token → expiry map, guarded by a mutex.
// Sessions do not survive restarts (acceptable for a dashboard).
type sessionStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{tokens: make(map[string]time.Time)}
}

// create issues a new random session token. When remember is true the token
// lives for 30 days, otherwise 8 hours.
func (s *sessionStore) create(remember bool) (token string, expiry time.Time) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read only fails when the OS RNG is broken; without randomness
		// we cannot issue safe tokens, so fail loudly.
		log.Fatalf("session: random token: %v", err)
	}
	expiry = time.Now().Add(sessionDuration)
	if remember {
		expiry = time.Now().Add(rememberDuration)
	}
	token = hex.EncodeToString(b[:])
	s.mu.Lock()
	s.tokens[token] = expiry
	s.mu.Unlock()
	return token, expiry
}

// valid reports whether token is a known, unexpired session. Expired tokens
// are removed lazily.
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.tokens[token]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(s.tokens, token)
		return false
	}
	return true
}

// revoke deletes a session token, if present.
func (s *sessionStore) revoke(token string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

// cleanupLoop periodically purges expired tokens. Run it in a goroutine.
func (s *sessionStore) cleanupLoop(interval time.Duration) {
	for range time.NewTicker(interval).C {
		now := time.Now()
		s.mu.Lock()
		for token, expiry := range s.tokens {
			if now.After(expiry) {
				delete(s.tokens, token)
			}
		}
		s.mu.Unlock()
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// loginHandler verifies the password with a constant-time compare and, on
// success, issues a session cookie. Any username is accepted; only the
// password is checked (same contract as HTTP Basic auth mode).
func (s *sessionStore) loginHandler(password string) http.HandlerFunc {
	want := sha256.Sum256([]byte(password))
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, sessionMaxBody)
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		got := sha256.Sum256([]byte(req.Password))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		token, expiry := s.create(req.Remember)
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			// NOTE: the Secure flag is intentionally not set — enable it by
			// terminating TLS in a reverse proxy and forwarding over HTTPS.
			MaxAge:  int(time.Until(expiry).Seconds()),
			Expires: expiry,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

// logoutHandler revokes the session token (if any) and clears the cookie.
func (s *sessionStore) logoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err == nil {
			s.revoke(c.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

// middleware enforces session auth for web-auth mode. The login/logout API,
// the public auth-status endpoint, and the static shell (dashboard HTML +
// favicon, which hosts the login overlay) stay public; every other request needs a valid session cookie.
// Unauthorized requests always get 401 JSON — never a redirect — so the
// frontend can show the login overlay without a redirect loop.
func (s *sessionStore) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" || r.URL.Path == "/api/logout" || r.URL.Path == "/api/auth" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/dumbdock.svg" {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(sessionCookieName)
		if err != nil || !s.valid(c.Value) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
