package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authCookieName   = "pkm_notes_session"
	authSessionTTL   = 12 * time.Hour
	authFailureTTL   = 15 * time.Minute
	authFailureLimit = 5
)

type loginFailures struct {
	count   int
	expires time.Time
}

type authenticator struct {
	password string
	key      []byte
	secure   bool
	mu       sync.Mutex
	failures map[string]loginFailures
}

func newAuthenticator(password string) (*authenticator, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &authenticator{
		password: password,
		key:      key,
		failures: make(map[string]loginFailures),
	}, nil
}

func randomHex(byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (a *authenticator) enabled() bool {
	return a.password != ""
}

func (a *authenticator) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled":       a.enabled(),
		"authenticated": !a.enabled() || a.authorized(r),
	})
}

func (a *authenticator) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !a.enabled() {
		http.Error(w, "authentication is not configured", http.StatusConflict)
		return
	}
	ip := clientIP(r)
	if a.isRateLimited(ip) {
		http.Error(w, "too many login attempts; try again later", http.StatusTooManyRequests)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(body.Password) > 4096 {
		http.Error(w, "password is too long", http.StatusBadRequest)
		return
	}
	expected := sha256.Sum256([]byte(a.password))
	provided := sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		a.recordFailure(ip)
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}
	a.clearFailures(ip)
	token, err := a.newSessionToken(time.Now())
	if err != nil {
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(authSessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (a *authenticator) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

func (a *authenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !a.enabled() {
			next.ServeHTTP(w, r)
			return
		}
		if (r.URL.Path == "/" || r.URL.Path == "/index.html") && !a.authorized(r) {
			http.Redirect(w, r, "/login.html", http.StatusSeeOther)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") &&
			r.URL.Path != "/api/auth/status" &&
			r.URL.Path != "/api/auth/login" &&
			r.URL.Path != "/api/auth/logout" &&
			r.URL.Path != "/api/health" &&
			!a.authorized(r) {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *authenticator) authorized(r *http.Request) bool {
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	return a.validSessionToken(cookie.Value, time.Now())
}

func (a *authenticator) newSessionToken(now time.Time) (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := strconv.FormatInt(now.Add(authSessionTTL).Unix(), 10) + ":" + hex.EncodeToString(nonce)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, nil
}

func (a *authenticator) validSessionToken(token string, now time.Time) bool {
	encoded, signature, found := strings.Cut(token, ".")
	if !found || encoded == "" || signature == "" {
		return false
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(encoded))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	expiryText, nonce, found := strings.Cut(string(raw), ":")
	if !found || nonce == "" {
		return false
	}
	expiry, err := strconv.ParseInt(expiryText, 10, 64)
	if err != nil {
		return false
	}
	expiration := time.Unix(expiry, 0)
	return expiration.After(now) && !expiration.After(now.Add(authSessionTTL+time.Minute))
}

func (a *authenticator) isRateLimited(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	failure, found := a.failures[ip]
	if !found || !failure.expires.After(now) {
		delete(a.failures, ip)
		return false
	}
	return failure.count >= authFailureLimit
}

func (a *authenticator) recordFailure(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for address, failure := range a.failures {
		if !failure.expires.After(now) {
			delete(a.failures, address)
		}
	}
	failure := a.failures[ip]
	if !failure.expires.After(now) {
		failure = loginFailures{expires: now.Add(authFailureTTL)}
	}
	failure.count++
	a.failures[ip] = failure
}

func (a *authenticator) clearFailures(ip string) {
	a.mu.Lock()
	delete(a.failures, ip)
	a.mu.Unlock()
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		if r.RemoteAddr == "" {
			return "unknown"
		}
		return r.RemoteAddr
	}
	return host
}
