package davidlloyd

import (
	"fmt"
	"sync"
	"time"

	"github.com/goosegit97/garmin-mcp/internal/atomfile"
)

// StoredSession is the persisted David Lloyd auth state: the exerciser id and the
// Netpulse session cookie obtained by the one-time `davidlloyd login` bootstrap.
// The username/password are NEVER stored — only this session token, which the
// provider can expire/revoke (so a leak is far less damaging than the password).
type StoredSession struct {
	UserID  string    `json:"user_id"`
	Cookie  string    `json:"cookie"`
	SavedAt time.Time `json:"saved_at"`

	// LastSyncedMeasuredAt/LastSyncedKg identify the most recent measurement
	// written to Garmin, so the daily sync job can skip re-writing the same
	// weigh-in. A re-login resets them, which only costs one redundant write.
	LastSyncedMeasuredAt string  `json:"last_synced_measured_at,omitempty"`
	LastSyncedKg         float64 `json:"last_synced_kg,omitempty"`
}

// SessionStore persists a StoredSession to a single file with 0600 permissions
// and atomic replacement (see atomfile). Safe for concurrent use.
type SessionStore struct {
	path string
	mu   sync.Mutex
}

// NewSessionStore returns a store backed by the given file path. The parent
// directory must already exist (the deploy provisions it).
func NewSessionStore(path string) *SessionStore {
	return &SessionStore{path: path}
}

// Load reads the persisted session. A missing file returns (nil, nil) so callers
// can distinguish "never logged in" from a real error.
func (s *SessionStore) Load() (*StoredSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sess StoredSession
	found, err := atomfile.ReadJSON(s.path, &sess)
	if err != nil {
		return nil, fmt.Errorf("david lloyd session store: %w", err)
	}
	if !found {
		return nil, nil
	}
	return &sess, nil
}

// Save writes the session atomically with 0600 permissions.
func (s *SessionStore) Save(sess *StoredSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := atomfile.WriteJSON(s.path, sess); err != nil {
		return fmt.Errorf("david lloyd session store: %w", err)
	}
	return nil
}
