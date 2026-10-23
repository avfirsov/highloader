package bootstrap

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Session struct {
	Origin, Epoch, Secret string
	cookie, csrf          string
	mu                    sync.Mutex
	tokens                map[string]time.Time
}

func Token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func NewSession(origin, epoch, secret string) (*Session, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" {
		return nil, fmt.Errorf("недопустимый origin")
	}
	cookie, err := Token()
	if err != nil {
		return nil, err
	}
	csrf, err := Token()
	if err != nil {
		return nil, err
	}
	return &Session{Origin: origin, Epoch: epoch, Secret: secret, cookie: cookie, csrf: csrf, tokens: map[string]time.Time{}}, nil
}

func (s *Session) LaunchURL() (string, error) {
	token, err := Token()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.tokens {
		if now.After(v) {
			delete(s.tokens, k)
		}
	}
	if len(s.tokens) >= 32 {
		return "", fmt.Errorf("слишком много ожидающих запусков браузера")
	}
	s.tokens[token] = now.Add(5 * time.Minute)
	return s.Origin + "/bootstrap?token=" + token, nil
}

func (s *Session) Handler(next http.Handler) http.Handler {
	host := s.Origin[len("http://"):]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Host != host {
			http.Error(w, "Недопустимый Host", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/internal/reopen" {
			if r.Method != "POST" || r.Header.Get("Origin") != "" || !equal(r.Header.Get("X-Highloader-Owner"), s.Secret) {
				http.Error(w, "Доступ запрещён", 403)
				return
			}
			address, err := s.LaunchURL()
			if err != nil {
				http.Error(w, "Не удалось открыть сессию", 429)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"epoch": s.Epoch, "url": address})
			return
		}
		if r.URL.Path == "/bootstrap" {
			if r.Method != "GET" {
				http.Error(w, "Метод не разрешён", 405)
				return
			}
			token := r.URL.Query().Get("token")
			s.mu.Lock()
			expiry, ok := s.tokens[token]
			delete(s.tokens, token)
			s.mu.Unlock()
			if !ok || time.Now().After(expiry) {
				http.Error(w, "Ссылка истекла. Запустите Highloader ещё раз.", 403)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "highloader_session", Value: s.cookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		cookie, err := r.Cookie("highloader_session")
		if err != nil || !equal(cookie.Value, s.cookie) {
			http.Error(w, "Откройте приложение через Highloader.exe", 401)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("Origin") != s.Origin || !equal(r.Header.Get("X-CSRF-Token"), s.csrf) {
				http.Error(w, "Проверка сессии не пройдена", 403)
				return
			}
		}
		if r.URL.Path == "/api/v1/session" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"epoch": s.Epoch, "csrfToken": s.csrf, "version": Version, "sourceDigest": SourceDigest})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func equal(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
