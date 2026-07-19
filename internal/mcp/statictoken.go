package mcp

import (
	"crypto/subtle"
	"net/http"
)

// StaticTokenMiddleware gates MCP traffic on a single shared bearer token. It is
// the single-user replacement for the multi-tenant OAuth2 resource-server flow:
// the deployment holds one secret (MCP_BEARER_TOKEN), and every request must
// present it. A missing or mismatched token yields 401 with a WWW-Authenticate
// challenge; the comparison is constant-time to avoid leaking the token by
// timing.
func StaticTokenMiddleware(token string) func(http.Handler) http.Handler {
	want := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearerToken(r)
			if !ok {
				staticChallenge(w, "")
				return
			}
			if subtle.ConstantTimeCompare([]byte(raw), want) != 1 {
				staticChallenge(w, "invalid_token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func staticChallenge(w http.ResponseWriter, errCode string) {
	challenge := `Bearer realm="garmin-mcp"`
	if errCode != "" {
		challenge += `, error="` + errCode + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
