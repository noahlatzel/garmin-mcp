package garmin

import (
	"fmt"
	"sync"

	"github.com/goosegit97/garmin-mcp/internal/atomfile"
)

// StoredTokens is the persisted Garmin auth state: the long-lived OAuth1 token
// (used to refresh) and the most recent OAuth2 access token. Credentials
// (username/password) are NEVER stored.
type StoredTokens struct {
	OAuth1 *OAuth1Token `json:"oauth1"`
	OAuth2 *OAuth2Token `json:"oauth2"`
}

// TokenStore persists StoredTokens to a single file with 0600 permissions and
// atomic replacement (see atomfile). It is safe for concurrent use.
type TokenStore struct {
	path string
	mu   sync.Mutex
}

// NewTokenStore returns a store backed by the given file path. The parent
// directory must already exist (the deploy provisions it).
func NewTokenStore(path string) *TokenStore {
	return &TokenStore{path: path}
}

// Load reads the persisted tokens. A missing file returns (nil, nil) so callers
// can distinguish "never logged in" from a real error.
func (s *TokenStore) Load() (*StoredTokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var tokens StoredTokens
	found, err := atomfile.ReadJSON(s.path, &tokens)
	if err != nil {
		return nil, fmt.Errorf("garmin token store: %w", err)
	}
	if !found {
		return nil, nil
	}
	return &tokens, nil
}

// Save writes the tokens atomically with 0600 permissions.
func (s *TokenStore) Save(tokens *StoredTokens) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := atomfile.WriteJSON(s.path, tokens); err != nil {
		return fmt.Errorf("garmin token store: %w", err)
	}
	return nil
}
