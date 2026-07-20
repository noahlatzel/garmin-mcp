package garmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// connectapi endpoint templates. Every path here is reached with the same auth as
// CreateWorkout: an OAuth2 Bearer + the Android UA (NOT OAuth1-signed). Paths were
// taken from python-garminconnect/garth and must be round-trip-verified live.
const (
	workoutHost  = connectAPIHost + "/workout-service"
	calendarHost = connectAPIHost + "/calendar-service"
	profileURL   = connectAPIHost + "/userprofile-service/socialProfile"
	summaryHost  = connectAPIHost + "/usersummary-service/usersummary/daily"
	wellnessHost = connectAPIHost + "/wellness-service/wellness"
	hrvHost      = connectAPIHost + "/hrv-service/hrv"
)

// authedJSON builds a connectapi request with the Bearer + Android UA + JSON
// headers, executes it via c.do, and returns the raw response body. A nil body
// makes a GET-style request with no Content-Type. It centralises the boilerplate
// shared by every connectapi call.
func (c *Client) authedJSON(ctx context.Context, bearer, method, reqURL string, body any) ([]byte, error) {
	if bearer == "" {
		return nil, fmt.Errorf("no access token; log in first")
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("User-Agent", androidUA)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	respBody, status, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("garmin %s %s (HTTP %d): %w", method, req.URL.Path, status, err)
	}
	return []byte(respBody), nil
}

// CreateWorkout POSTs a built Workout (strength or pool swim) to Garmin Connect
// using an OAuth2 bearer, returning the raw created-workout JSON (which carries
// the assigned workoutId). The OAuth flow yields a connectapi.garmin.com bearer
// (vs the cookie-authenticated connect.garmin.com/gc-api path the schema was
// harvested from); the envelope is identical. The caller is responsible for
// supplying a non-expired bearer.
func (c *Client) CreateWorkout(ctx context.Context, bearer string, w *Workout) ([]byte, error) {
	return c.authedJSON(ctx, bearer, http.MethodPost, workoutHost+"/workout", w)
}

// ScheduleWorkout puts an existing workout on the Garmin calendar for the given
// date (YYYY-MM-DD), returning the schedule JSON (carrying a workoutScheduleId).
func (c *Client) ScheduleWorkout(ctx context.Context, bearer, workoutID, date string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/schedule/%s", workoutHost, url.PathEscape(workoutID))
	return c.authedJSON(ctx, bearer, http.MethodPost, reqURL, map[string]string{"date": date})
}

// ListWorkouts returns the user's saved workouts (id, name, type), newest first,
// up to limit entries.
func (c *Client) ListWorkouts(ctx context.Context, bearer string, limit int) ([]byte, error) {
	if limit <= 0 {
		limit = 20
	}
	reqURL := fmt.Sprintf("%s/workouts?start=0&limit=%d&myWorkoutsOnly=true", workoutHost, limit)
	return c.authedJSON(ctx, bearer, http.MethodGet, reqURL, nil)
}

// GetWorkout fetches one workout's full JSON by id.
func (c *Client) GetWorkout(ctx context.Context, bearer, workoutID string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/workout/%s", workoutHost, url.PathEscape(workoutID))
	return c.authedJSON(ctx, bearer, http.MethodGet, reqURL, nil)
}

// DeleteWorkout deletes one workout by id.
func (c *Client) DeleteWorkout(ctx context.Context, bearer, workoutID string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/workout/%s", workoutHost, url.PathEscape(workoutID))
	return c.authedJSON(ctx, bearer, http.MethodDelete, reqURL, nil)
}

// UnscheduleWorkout removes one scheduled calendar entry by its
// workoutScheduleId (the id returned by ScheduleWorkout / carried on calendar
// items). The saved workout itself is untouched. Path taken from
// python-garminconnect and must be round-trip-verified live (garmin verify batch).
func (c *Client) UnscheduleWorkout(ctx context.Context, bearer, scheduleID string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/schedule/%s", workoutHost, url.PathEscape(scheduleID))
	return c.authedJSON(ctx, bearer, http.MethodDelete, reqURL, nil)
}

// UpdateWorkout PUTs a full workout JSON body back to Garmin Connect for the
// given id. Callers must send a complete read-modify-write body (never a
// partial document, which could blank fields).
func (c *Client) UpdateWorkout(ctx context.Context, bearer, workoutID string, body json.RawMessage) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/workout/%s", workoutHost, url.PathEscape(workoutID))
	return c.authedJSON(ctx, bearer, http.MethodPut, reqURL, body)
}

// GetCalendar reads scheduled items for a month. month is 0-indexed (Jan=0) to
// match the Garmin calendar-service contract.
func (c *Client) GetCalendar(ctx context.Context, bearer string, year, month int) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/year/%d/month/%d", calendarHost, year, month)
	return c.authedJSON(ctx, bearer, http.MethodGet, reqURL, nil)
}

// GetProfile fetches the social profile (carrying displayName, needed for the
// sleep and daily-summary paths).
func (c *Client) GetProfile(ctx context.Context, bearer string) ([]byte, error) {
	return c.authedJSON(ctx, bearer, http.MethodGet, profileURL, nil)
}

// GetDailySummary returns steps, calories, intensity minutes, resting HR and body
// battery range for a day. displayName is embedded in the path.
func (c *Client) GetDailySummary(ctx context.Context, bearer, displayName, date string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/%s?calendarDate=%s", summaryHost, url.PathEscape(displayName), url.QueryEscape(date))
	return c.authedJSON(ctx, bearer, http.MethodGet, reqURL, nil)
}

// GetSleep returns sleep stages, duration and sleep score for a date. displayName
// is embedded in the path.
func (c *Client) GetSleep(ctx context.Context, bearer, displayName, date string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/dailySleepData/%s?date=%s&nonSleepBufferMinutes=60", wellnessHost, url.PathEscape(displayName), url.QueryEscape(date))
	return c.authedJSON(ctx, bearer, http.MethodGet, reqURL, nil)
}

// GetHRV returns overnight HRV (status + readings) for a date. The bearer
// identifies the user, so no displayName is needed.
func (c *Client) GetHRV(ctx context.Context, bearer, date string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/%s", hrvHost, url.PathEscape(date))
	return c.authedJSON(ctx, bearer, http.MethodGet, reqURL, nil)
}

// GetStress returns the daily stress timeline for a date.
func (c *Client) GetStress(ctx context.Context, bearer, date string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/dailyStress/%s", wellnessHost, url.PathEscape(date))
	return c.authedJSON(ctx, bearer, http.MethodGet, reqURL, nil)
}

// GetBodyBattery returns the body-battery timeline over an inclusive date range.
func (c *Client) GetBodyBattery(ctx context.Context, bearer, start, end string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/bodyBattery/reports/daily?startDate=%s&endDate=%s", wellnessHost, url.QueryEscape(start), url.QueryEscape(end))
	return c.authedJSON(ctx, bearer, http.MethodGet, reqURL, nil)
}

// workoutIDFromCreate extracts the assigned workoutId from a create-workout
// response so a follow-up schedule call can target it.
func workoutIDFromCreate(created []byte) (string, error) {
	var env struct {
		WorkoutID json.Number `json:"workoutId"`
	}
	if err := json.Unmarshal(created, &env); err != nil {
		return "", fmt.Errorf("parsing created workout: %w", err)
	}
	if env.WorkoutID == "" {
		return "", fmt.Errorf("created workout response had no workoutId")
	}
	return env.WorkoutID.String(), nil
}
