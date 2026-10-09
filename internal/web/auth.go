package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/fenixstarlord/indexserver/internal/store"
)

const (
	sessionCookie = "replicant_session"
	sessionTTL    = 30 * 24 * time.Hour
	secretSetting = "session_secret"
)

type ctxKey int

const identityKey ctxKey = 1

// Identity describes who made an authenticated request.
type Identity struct {
	Auth      string // "token", "session", or "open" (no auth configured)
	TokenName string
}

// IdentityFrom returns the request's identity, if authenticated.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey).(Identity)
	return id, ok
}

// loadSessionSecret returns the configured secret or a persisted
// auto-generated one.
func loadSessionSecret(ctx context.Context, st *store.Store, configured string) ([]byte, error) {
	if configured != "" {
		return []byte(configured), nil
	}
	v, err := st.GetSetting(ctx, secretSetting)
	if err != nil {
		return nil, err
	}
	if v == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		v = hex.EncodeToString(raw)
		if err := st.SetSetting(ctx, secretSetting, v); err != nil {
			return nil, err
		}
	}
	return []byte(v), nil
}

// checkPassword compares a submitted password with the configured one.
func (s *Server) checkPassword(submitted string) bool {
	if s.cfg.PasswordHash != "" {
		return bcrypt.CompareHashAndPassword([]byte(s.cfg.PasswordHash), []byte(submitted)) == nil
	}
	if s.cfg.Password == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.cfg.Password), []byte(submitted)) == 1
}

func (s *Server) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// newSessionValue returns "expiry.signature".
func (s *Server) newSessionValue(now time.Time) string {
	exp := strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	return exp + "." + s.sign(exp)
}

func (s *Server) validSession(value string, now time.Time) bool {
	exp, sig, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(s.sign(exp))) {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	return err == nil && now.Unix() < n
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: s.newSessionValue(time.Now()), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		MaxAge: int(sessionTTL.Seconds()),
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

var errUnauthenticated = errors.New("unauthenticated")

// authenticate resolves a bearer token or session cookie.
func (s *Server) authenticate(r *http.Request) (Identity, error) {
	if s.cfg.Open {
		return Identity{Auth: "open", TokenName: "local"}, nil
	}
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, token, ok := strings.Cut(h, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			return Identity{}, fmt.Errorf("malformed Authorization header")
		}
		tok, ok, err := s.store.VerifyToken(r.Context(), strings.TrimSpace(token))
		if err != nil {
			return Identity{}, err
		}
		if !ok {
			return Identity{}, errUnauthenticated
		}
		return Identity{Auth: "token", TokenName: tok.Name}, nil
	}
	if c, err := r.Cookie(sessionCookie); err == nil && s.validSession(c.Value, time.Now()) {
		return Identity{Auth: "session"}, nil
	}
	return Identity{}, errUnauthenticated
}

// requireAuth wraps a handler so only authenticated requests reach it.
// API paths get a JSON 401; everything else is redirected to the login page.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := s.authenticate(r)
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") || r.Header.Get("Authorization") != "" {
				status := http.StatusUnauthorized
				msg := "authentication required"
				if !errors.Is(err, errUnauthenticated) {
					msg = err.Error()
					if !strings.Contains(msg, "Authorization") {
						status = http.StatusInternalServerError
					}
				}
				writeError(w, status, msg)
				return
			}
			http.Redirect(w, r, "/login?next="+r.URL.RequestURI(), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey, id)))
	})
}
