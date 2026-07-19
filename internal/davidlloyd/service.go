package davidlloyd

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// errUnauthorized signals an expired/invalid session.
var errUnauthorized = errors.New("david lloyd session unauthorized")

// ErrNotLinked is returned when no session has been bootstrapped yet (run
// `davidlloyd login`).
var ErrNotLinked = errors.New("david lloyd is not linked yet: run the one-time `davidlloyd login` bootstrap")

// Service reads David Lloyd body measurements using a persisted session token. It
// never holds the password: the password is used once by Bootstrap (the
// human-gated `davidlloyd login`) and only the resulting session token is stored.
// If the session expires, reads fail with a clear "re-run login" error rather
// than silently re-authenticating (there is no stored password to do so).
type Service struct {
	client *Client
	store  *SessionStore
}

// NewService builds a Service backed by a session store at path.
func NewService(sessionPath string) *Service {
	return &Service{client: NewClient(), store: NewSessionStore(sessionPath)}
}

// NewServiceWithClient is NewService with an injected Client (tests).
func NewServiceWithClient(c *Client, sessionPath string) *Service {
	return &Service{client: c, store: NewSessionStore(sessionPath)}
}

// Bootstrap performs the one-time credentialed login and persists ONLY the
// resulting session token. brand defaults to "davidlloydclubs" when empty. It is
// invoked only by the human-gated `davidlloyd login` path; the credentials are
// not retained.
func (s *Service) Bootstrap(ctx context.Context, brand, username, password string) error {
	if brand == "" {
		brand = "davidlloydclubs"
	}
	sess, err := s.client.Login(ctx, brand, username, password)
	if err != nil {
		return err
	}
	return s.store.Save(&StoredSession{
		UserID:  sess.UserID,
		Cookie:  sess.Cookie,
		SavedAt: time.Now().UTC(),
	})
}

// Weight is the latest body-weight measurement.
type Weight struct {
	Kg         float64 `json:"kg"`
	MeasuredAt string  `json:"measured_at"` // ISO-8601 from the source (createdAt)
	Source     string  `json:"source"`      // e.g. "Tanita"
}

// LatestWeight returns the most recent body weight (kg) for the linked account.
func (s *Service) LatestWeight(ctx context.Context) (*Weight, error) {
	metrics, err := s.LatestBody(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range metrics {
		if m.Type == WeightType {
			return &Weight{Kg: m.Value, MeasuredAt: m.CreatedAt, Source: m.SourceLabel}, nil
		}
	}
	return nil, fmt.Errorf("no %s measurement found for this account", WeightType)
}

// LatestBody returns the full latest body-composition panel.
func (s *Service) LatestBody(ctx context.Context) ([]BodyMetric, error) {
	stored, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, ErrNotLinked
	}
	metrics, err := s.client.LatestBody(ctx, &Session{UserID: stored.UserID, Cookie: stored.Cookie})
	if errors.Is(err, errUnauthorized) {
		return nil, fmt.Errorf("%w: session expired (saved %s); re-run `davidlloyd login`",
			ErrNotLinked, stored.SavedAt.Format(time.RFC3339))
	}
	if err != nil {
		return nil, err
	}
	return metrics, nil
}
