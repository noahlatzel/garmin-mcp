package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// bearerToken extracts the token from an Authorization header, if present and
// well-formed.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(auth[len(prefix):]), true
}

// HealthHandler returns an unauthenticated health handler for /healthz, useful
// behind Caddy/systemd. It never exposes data. When healthy it responds 200 with
// {"status":"ok","service":<service>}; if any check returns an error it responds
// 503 with {"status":"error","service":<service>,"reason":<detail>}. Checks are
// for hard service dependencies (e.g. a database ping) — not per-user state.
func HealthHandler(service string, checks ...func(context.Context) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for _, check := range checks {
			if err := check(r.Context()); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"status": "error", "service": service, "reason": err.Error(),
				})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": service})
	})
}
