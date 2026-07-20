package garmin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Service ties the exercise catalog, the auth Client, and the token store into
// the operations the MCP layer exposes. It refreshes the OAuth2 token on demand
// from the stored OAuth1 token, never performing a credentialed login itself
// (that is the human-gated bootstrap; see cmd/garmin login).
type Service struct {
	Catalog *Catalog
	client  *Client
	store   *TokenStore
	mu      sync.Mutex

	// displayNameCache holds the user's Garmin displayName once fetched; the
	// sleep and daily-summary paths embed it. Guarded by mu.
	displayNameCache string
}

// NewService builds a Service from a token store path, loading the embedded
// catalog and a default (stdlib) auth client.
func NewService(tokenPath string) (*Service, error) {
	cat, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	client, err := NewClient()
	if err != nil {
		return nil, err
	}
	return &Service{
		Catalog: cat,
		client:  client,
		store:   NewTokenStore(tokenPath),
	}, nil
}

// bearer returns a valid OAuth2 access token, refreshing from the stored OAuth1
// token if the access token is missing or expired and persisting the refreshed
// token. It returns a clear error if no login has been bootstrapped.
func (s *Service) bearer(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens, err := s.store.Load()
	if err != nil {
		return "", err
	}
	if tokens == nil || tokens.OAuth1 == nil {
		return "", fmt.Errorf("garmin is not linked yet: run the one-time `garmin login` bootstrap")
	}
	if !tokens.OAuth2.Expired() {
		return tokens.OAuth2.AccessToken, nil
	}

	refreshed, err := s.client.Refresh(ctx, tokens.OAuth1)
	if err != nil {
		return "", fmt.Errorf("refreshing Garmin access token: %w", err)
	}
	tokens.OAuth2 = refreshed
	if err := s.store.Save(tokens); err != nil {
		return "", fmt.Errorf("persisting refreshed token: %w", err)
	}
	return refreshed.AccessToken, nil
}

// CreateStrengthWorkout builds a strength workout from the spec and writes it
// to Garmin Connect, returning the created-workout JSON. If scheduleDate is
// non-empty (YYYY-MM-DD), the created workout is also scheduled on the Garmin
// calendar for that date and the returned JSON gains a "schedule" field.
func (s *Service) CreateStrengthWorkout(ctx context.Context, name string, specs []StepSpec, scheduleDate string) ([]byte, error) {
	workout, err := s.Catalog.BuildWorkout(name, specs)
	if err != nil {
		return nil, err
	}
	return s.createWorkout(ctx, workout, scheduleDate)
}

// CreateSwimWorkout builds a pool-swim workout from the spec and writes it to
// Garmin Connect, returning the created-workout JSON. If scheduleDate is
// non-empty (YYYY-MM-DD), the created workout is also scheduled and the
// returned JSON gains a "schedule" field.
func (s *Service) CreateSwimWorkout(ctx context.Context, name string, poolLengthMeters float64, specs []SwimStepSpec, scheduleDate string) ([]byte, error) {
	workout, err := BuildSwimWorkout(name, poolLengthMeters, specs)
	if err != nil {
		return nil, err
	}
	return s.createWorkout(ctx, workout, scheduleDate)
}

// CreateBuilt writes an already-built workout (strength or swim) to Garmin
// Connect, optionally scheduling it. It exists so the batch tool can validate
// and build every item before the first network call, then reuse the exact
// create+schedule path of the single-workout tools.
func (s *Service) CreateBuilt(ctx context.Context, workout *Workout, scheduleDate string) ([]byte, error) {
	return s.createWorkout(ctx, workout, scheduleDate)
}

// ScheduleFailedError reports a workout that was created but could not be
// scheduled, carrying the assigned id so the caller can schedule it separately
// instead of re-creating it.
type ScheduleFailedError struct {
	WorkoutID string
	Err       error
}

func (e *ScheduleFailedError) Error() string {
	return fmt.Sprintf("workout %s created but scheduling failed: %v", e.WorkoutID, e.Err)
}

func (e *ScheduleFailedError) Unwrap() error { return e.Err }

// createWorkout ensures a valid bearer, writes a built workout, and — when
// scheduleDate is non-empty — schedules it, attaching the schedule response to
// the returned created-workout JSON.
func (s *Service) createWorkout(ctx context.Context, workout *Workout, scheduleDate string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	created, err := s.client.CreateWorkout(ctx, bearer, workout)
	if err != nil {
		return nil, err
	}
	if scheduleDate == "" {
		return created, nil
	}

	workoutID, err := workoutIDFromCreate(created)
	if err != nil {
		return nil, err
	}
	schedule, err := s.client.ScheduleWorkout(ctx, bearer, workoutID, scheduleDate)
	if err != nil {
		return nil, &ScheduleFailedError{WorkoutID: workoutID, Err: err}
	}
	return mergeCreateSchedule(created, schedule)
}

// ScheduleWorkout schedules an existing workout on the Garmin calendar.
func (s *Service) ScheduleWorkout(ctx context.Context, workoutID, date string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.ScheduleWorkout(ctx, bearer, workoutID, date)
}

// ListWorkouts lists the user's saved workouts (up to limit).
func (s *Service) ListWorkouts(ctx context.Context, limit int) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.ListWorkouts(ctx, bearer, limit)
}

// GetWorkout fetches one workout's JSON by id.
func (s *Service) GetWorkout(ctx context.Context, workoutID string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.GetWorkout(ctx, bearer, workoutID)
}

// UnscheduleWorkout removes one scheduled calendar entry by its
// workoutScheduleId; the saved workout is untouched.
func (s *Service) UnscheduleWorkout(ctx context.Context, scheduleID string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.UnscheduleWorkout(ctx, bearer, scheduleID)
}

// RenameWorkout renames one saved workout via read-modify-write: it fetches
// the full workout JSON, changes only workoutName, PUTs the complete body
// back, then re-fetches and verifies the new name took. It returns the
// previous name.
func (s *Service) RenameWorkout(ctx context.Context, workoutID, newName string) (string, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return "", err
	}
	current, err := s.client.GetWorkout(ctx, bearer, workoutID)
	if err != nil {
		return "", err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(current, &obj); err != nil {
		return "", fmt.Errorf("parsing workout %s: %w", workoutID, err)
	}
	var oldName string
	if raw, ok := obj["workoutName"]; ok {
		if err := json.Unmarshal(raw, &oldName); err != nil {
			return "", fmt.Errorf("parsing workoutName of %s: %w", workoutID, err)
		}
	}
	encoded, err := json.Marshal(newName)
	if err != nil {
		return "", fmt.Errorf("encoding new name: %w", err)
	}
	obj["workoutName"] = encoded
	body, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("encoding workout %s: %w", workoutID, err)
	}
	if _, err := s.client.UpdateWorkout(ctx, bearer, workoutID, body); err != nil {
		return "", err
	}

	after, err := s.client.GetWorkout(ctx, bearer, workoutID)
	if err != nil {
		return "", fmt.Errorf("rename applied but verification fetch failed: %w", err)
	}
	var check struct {
		WorkoutName string `json:"workoutName"`
	}
	if err := json.Unmarshal(after, &check); err != nil {
		return "", fmt.Errorf("rename applied but verification parse failed: %w", err)
	}
	if check.WorkoutName != newName {
		return "", fmt.Errorf("rename did not take: workout %s still named %q", workoutID, check.WorkoutName)
	}
	return oldName, nil
}

// DeleteWorkout deletes one workout by id.
func (s *Service) DeleteWorkout(ctx context.Context, workoutID string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.DeleteWorkout(ctx, bearer, workoutID)
}

// GetCalendar reads scheduled items for a 0-indexed month.
func (s *Service) GetCalendar(ctx context.Context, year, month int) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.GetCalendar(ctx, bearer, year, month)
}

// GetDailySummary returns the day's activity/health summary.
func (s *Service) GetDailySummary(ctx context.Context, date string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	name, err := s.displayName(ctx, bearer)
	if err != nil {
		return nil, err
	}
	return s.client.GetDailySummary(ctx, bearer, name, date)
}

// GetSleep returns sleep stages, duration and score for a date.
func (s *Service) GetSleep(ctx context.Context, date string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	name, err := s.displayName(ctx, bearer)
	if err != nil {
		return nil, err
	}
	return s.client.GetSleep(ctx, bearer, name, date)
}

// GetHRV returns overnight HRV for a date.
func (s *Service) GetHRV(ctx context.Context, date string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.GetHRV(ctx, bearer, date)
}

// GetStress returns the daily stress timeline for a date.
func (s *Service) GetStress(ctx context.Context, date string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.GetStress(ctx, bearer, date)
}

// GetBodyBattery returns the body-battery timeline over a date range.
func (s *Service) GetBodyBattery(ctx context.Context, start, end string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.GetBodyBattery(ctx, bearer, start, end)
}

// displayName returns the user's Garmin displayName, fetching the social profile
// once and caching it. Sleep and daily-summary embed it in their paths.
func (s *Service) displayName(ctx context.Context, bearer string) (string, error) {
	s.mu.Lock()
	cached := s.displayNameCache
	s.mu.Unlock()
	if cached != "" {
		return cached, nil
	}

	body, err := s.client.GetProfile(ctx, bearer)
	if err != nil {
		return "", err
	}
	var profile struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return "", fmt.Errorf("parsing social profile: %w", err)
	}
	if profile.DisplayName == "" {
		return "", fmt.Errorf("social profile had no displayName")
	}

	s.mu.Lock()
	s.displayNameCache = profile.DisplayName
	s.mu.Unlock()
	return profile.DisplayName, nil
}

// mergeCreateSchedule returns the created-workout JSON with the schedule response
// attached under a "schedule" key, so a create+schedule call reports both.
func mergeCreateSchedule(created, schedule []byte) ([]byte, error) {
	var createdObj map[string]json.RawMessage
	if err := json.Unmarshal(created, &createdObj); err != nil {
		return nil, fmt.Errorf("parsing created workout: %w", err)
	}
	createdObj["schedule"] = json.RawMessage(schedule)
	merged, err := json.Marshal(createdObj)
	if err != nil {
		return nil, fmt.Errorf("encoding create+schedule result: %w", err)
	}
	return merged, nil
}

// Bootstrap performs the one-time credentialed login and persists the resulting
// tokens. It is invoked only by the human-gated `garmin login` path, never by
// the running MCP server.
func (s *Service) Bootstrap(ctx context.Context, creds Credentials) error {
	oauth1, oauth2, err := s.client.Login(ctx, creds)
	if err != nil {
		return err
	}
	return s.store.Save(&StoredTokens{OAuth1: oauth1, OAuth2: oauth2})
}
