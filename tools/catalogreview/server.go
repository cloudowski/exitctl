package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"html"
	"net"
	"net/http"
	"strings"
)

//go:embed index.html
var indexHTML string

// server exposes the store as a small JSON API plus the review page. It only
// answers requests addressed to the local host name it was started with and
// carrying the per-run token, so other web pages cannot drive it (no CSRF,
// no DNS rebinding).
type server struct {
	store *Store
	token string
	hosts map[string]bool
}

func newServer(st *Store, token, addr string) *server {
	_, port, _ := net.SplitHostPort(addr)
	return &server{store: st, token: token, hosts: map[string]bool{
		"127.0.0.1:" + port: true, "localhost:" + port: true, "[::1]:" + port: true,
	}}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /api/state", s.api(func(r *http.Request) (any, error) { return s.store.State() }))
	mux.HandleFunc("GET /api/entry", s.api(func(r *http.Request) (any, error) {
		return s.store.Detail(r.URL.Query().Get("file"))
	}))
	mux.HandleFunc("POST /api/status", s.api(func(r *http.Request) (any, error) {
		var req struct {
			Files  []string `json:"files"`
			Status string   `json:"status"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.store.SetStatus(req.Files, req.Status)
	}))
	mux.HandleFunc("POST /api/recommend", s.api(func(r *http.Request) (any, error) {
		var req struct {
			File string `json:"file"`
			Ref  string `json:"ref"`
			On   bool   `json:"on"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.store.SetRecommended(req.File, req.Ref, req.On)
	}))
	mux.HandleFunc("POST /api/trait-level", s.api(func(r *http.Request) (any, error) {
		var req struct {
			File  string `json:"file"`
			Level string `json:"level"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.store.SetTraitLevel(req.File, req.Level)
	}))
	mux.HandleFunc("POST /api/trait-ref-level", s.api(func(r *http.Request) (any, error) {
		var req struct {
			File   string `json:"file"`
			Trait  string `json:"trait"`
			Level  string `json:"level"`
			Reason string `json:"reason"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.store.SetTraitRefLevel(req.File, req.Trait, req.Level, req.Reason)
	}))
	mux.HandleFunc("POST /api/remove-mapping", s.api(func(r *http.Request) (any, error) {
		var req struct {
			File string `json:"file"`
			Ref  string `json:"ref"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.store.RemoveMapping(req.File, req.Ref)
	}))
	return s.guard(mux)
}

func ok() map[string]bool { return map[string]bool{"ok": true} }

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// guard rejects requests for other host names and API calls without the token.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Review-Token")), []byte(s.token)) != 1 {
			http.Error(w, "missing or wrong token", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:")
	_, _ = w.Write([]byte(strings.Replace(indexHTML, "{{TOKEN}}", html.EscapeString(s.token), 1)))
}

func (s *server) api(fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			code := http.StatusBadRequest
			body := map[string]any{"error": err.Error()}
			var ve *ValidationError
			if errors.As(err, &ve) {
				code = http.StatusConflict
				body["error"] = "The change was rolled back because it would make the catalog invalid."
				body["problems"] = ve.Problems
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(body)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
}
