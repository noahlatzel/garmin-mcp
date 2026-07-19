package garmin

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// recordDoer records each request and replies with a canned response keyed by a
// substring of the path (first match wins), defaulting to 200 "{}".
type recordDoer struct {
	t         *testing.T
	routes    map[string]string // path substring -> response body
	reqs      []recordedReq
	failOnAny bool // when true, any call fails the test (asserts no HTTP made)
}

type recordedReq struct {
	method string
	path   string
	query  string
	body   string
}

func (d *recordDoer) Do(req *http.Request) (*http.Response, error) {
	if d.failOnAny {
		d.t.Fatalf("unexpected HTTP call: %s %s", req.Method, req.URL)
	}
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	d.reqs = append(d.reqs, recordedReq{req.Method, req.URL.Path, req.URL.RawQuery, body})

	if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
		d.t.Errorf("%s %s missing Bearer auth: %q", req.Method, req.URL.Path, got)
	}
	if got := req.Header.Get("User-Agent"); got != androidUA {
		d.t.Errorf("%s %s wrong UA: %q", req.Method, req.URL.Path, got)
	}

	for sub, resp := range d.routes {
		if strings.Contains(req.URL.Path, sub) {
			return jsonResp(200, resp), nil
		}
	}
	return jsonResp(200, "{}"), nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func (d *recordDoer) last() recordedReq {
	d.t.Helper()
	if len(d.reqs) == 0 {
		d.t.Fatal("no request recorded")
	}
	return d.reqs[len(d.reqs)-1]
}

// TestClientMethods asserts each connectapi method hits the right method+path+body.
func TestClientMethods(t *testing.T) {
	d := &recordDoer{t: t, routes: map[string]string{}}
	c := NewClientWithDoer(d)
	ctx := context.Background()
	const b = "tok"

	tests := []struct {
		name       string
		call       func() ([]byte, error)
		wantMethod string
		wantPath   string
		wantQuery  string // substring, "" to skip
		wantBody   string // substring, "" to skip
	}{
		{"schedule", func() ([]byte, error) { return c.ScheduleWorkout(ctx, b, "123", "2030-01-02") },
			http.MethodPost, "/workout-service/schedule/123", "", `"date":"2030-01-02"`},
		{"list", func() ([]byte, error) { return c.ListWorkouts(ctx, b, 5) },
			http.MethodGet, "/workout-service/workouts", "limit=5", ""},
		{"get", func() ([]byte, error) { return c.GetWorkout(ctx, b, "123") },
			http.MethodGet, "/workout-service/workout/123", "", ""},
		{"delete", func() ([]byte, error) { return c.DeleteWorkout(ctx, b, "123") },
			http.MethodDelete, "/workout-service/workout/123", "", ""},
		{"calendar", func() ([]byte, error) { return c.GetCalendar(ctx, b, 2030, 0) },
			http.MethodGet, "/calendar-service/year/2030/month/0", "", ""},
		{"profile", func() ([]byte, error) { return c.GetProfile(ctx, b) },
			http.MethodGet, "/userprofile-service/socialProfile", "", ""},
		{"summary", func() ([]byte, error) { return c.GetDailySummary(ctx, b, "me", "2030-01-02") },
			http.MethodGet, "/usersummary-service/usersummary/daily/me", "calendarDate=2030-01-02", ""},
		{"sleep", func() ([]byte, error) { return c.GetSleep(ctx, b, "me", "2030-01-02") },
			http.MethodGet, "/wellness-service/wellness/dailySleepData/me", "date=2030-01-02", ""},
		{"hrv", func() ([]byte, error) { return c.GetHRV(ctx, b, "2030-01-02") },
			http.MethodGet, "/hrv-service/hrv/2030-01-02", "", ""},
		{"stress", func() ([]byte, error) { return c.GetStress(ctx, b, "2030-01-02") },
			http.MethodGet, "/wellness-service/wellness/dailyStress/2030-01-02", "", ""},
		{"bodybattery", func() ([]byte, error) { return c.GetBodyBattery(ctx, b, "2030-01-01", "2030-01-07") },
			http.MethodGet, "/wellness-service/wellness/bodyBattery/reports/daily", "startDate=2030-01-01", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.call(); err != nil {
				t.Fatalf("call: %v", err)
			}
			got := d.last()
			if got.method != tt.wantMethod {
				t.Errorf("method = %s, want %s", got.method, tt.wantMethod)
			}
			if got.path != tt.wantPath {
				t.Errorf("path = %s, want %s", got.path, tt.wantPath)
			}
			if tt.wantQuery != "" && !strings.Contains(got.query, tt.wantQuery) {
				t.Errorf("query = %s, want substring %s", got.query, tt.wantQuery)
			}
			if tt.wantBody != "" && !strings.Contains(got.body, tt.wantBody) {
				t.Errorf("body = %s, want substring %s", got.body, tt.wantBody)
			}
		})
	}
}

// testService builds a Service whose store holds a non-expired bearer, backed by
// the given doer.
func testService(t *testing.T, d HTTPDoer) *Service {
	t.Helper()
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	store := NewTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err := store.Save(&StoredTokens{
		OAuth1: &OAuth1Token{Token: "ot", TokenSecret: "ots"},
		OAuth2: &OAuth2Token{AccessToken: "AT", ExpiresAt: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}
	return &Service{Catalog: cat, client: NewClientWithDoer(d), store: store}
}

// TestCreateSchedule_OneCall asserts create+schedule issues both calls and merges
// the schedule into the result.
func TestCreateSchedule_OneCall(t *testing.T) {
	d := &recordDoer{t: t, routes: map[string]string{
		"/workout-service/workout":  `{"workoutId":987}`,
		"/workout-service/schedule": `{"workoutScheduleId":555}`,
	}}
	svc := testService(t, d)

	weight := 10.0
	out, err := svc.CreateStrengthWorkout(context.Background(), "x",
		[]StepSpec{{Exercise: "BARBELL_BICEPS_CURL", Sets: 1, Reps: 5, WeightKg: &weight}}, "2030-05-06")
	if err != nil {
		t.Fatalf("CreateStrengthWorkout: %v", err)
	}
	if !strings.Contains(string(out), `"workoutScheduleId":555`) || !strings.Contains(string(out), `"schedule"`) {
		t.Fatalf("result missing merged schedule: %s", out)
	}
	// Two calls: create then schedule for the harvested workoutId.
	if len(d.reqs) != 2 {
		t.Fatalf("want 2 requests, got %d", len(d.reqs))
	}
	if !strings.Contains(d.reqs[1].path, "/schedule/987") {
		t.Errorf("schedule did not target created workoutId: %s", d.reqs[1].path)
	}
}

// TestDisplayNameCache asserts the social profile is fetched once and reused.
func TestDisplayNameCache(t *testing.T) {
	d := &recordDoer{t: t, routes: map[string]string{
		"socialProfile": `{"displayName":"goose"}`,
	}}
	svc := testService(t, d)
	ctx := context.Background()

	if _, err := svc.GetSleep(ctx, "2030-01-02"); err != nil {
		t.Fatalf("GetSleep: %v", err)
	}
	if _, err := svc.GetDailySummary(ctx, "2030-01-02"); err != nil {
		t.Fatalf("GetDailySummary: %v", err)
	}
	var profileHits int
	for _, r := range d.reqs {
		if strings.Contains(r.path, "socialProfile") {
			profileHits++
		}
	}
	if profileHits != 1 {
		t.Fatalf("socialProfile fetched %d times, want 1 (cached)", profileHits)
	}
}

// TestGating_NoHTTP asserts dry_run/confirm short-circuit before any HTTP call.
func TestGating_NoHTTP(t *testing.T) {
	d := &recordDoer{t: t, failOnAny: true}
	svc := testService(t, d)
	ctx := context.Background()

	// schedule_workout dry_run: returns the planned request, makes no call.
	res, _, err := handleScheduleWorkout(svc)(ctx, nil, ScheduleWorkoutInput{WorkoutID: "1", Date: "2030-01-02", DryRun: true})
	if err != nil {
		t.Fatalf("schedule dry_run: %v", err)
	}
	assertText(t, res, "dry_run")

	// create_strength_workout dry_run: builds locally, makes no call.
	res, _, err = handleCreateStrengthWorkout(svc)(ctx, nil, CreateStrengthWorkoutInput{
		Name: "x", Steps: []StepInput{{Exercise: "BARBELL_BICEPS_CURL", Reps: 5}}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("create dry_run: %v", err)
	}
	assertText(t, res, "dry_run")
}

// TestScheduleWorkout_BadDate rejects a malformed date before any call.
func TestScheduleWorkout_BadDate(t *testing.T) {
	d := &recordDoer{t: t, failOnAny: true}
	svc := testService(t, d)
	if _, _, err := handleScheduleWorkout(svc)(context.Background(), nil,
		ScheduleWorkoutInput{WorkoutID: "1", Date: "05/06/2030"}); err == nil {
		t.Fatal("expected error for malformed date")
	}
}

func assertText(t *testing.T, res *mcp.CallToolResult, want string) {
	t.Helper()
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, want) {
			return
		}
	}
	t.Fatalf("result did not contain %q: %+v", want, res.Content)
}
