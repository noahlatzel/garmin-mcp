package garmin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// scriptedDoer replies like recordDoer but lets a test fail the nth request
// matching a path substring, to exercise mid-batch network failures.
type scriptedDoer struct {
	t       *testing.T
	routes  map[string]string
	reqs    []recordedReq
	failSub string // path substring whose nth match gets a 500
	failNth int    // 1-indexed
	seenSub int
}

func (d *scriptedDoer) Do(req *http.Request) (*http.Response, error) {
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	d.reqs = append(d.reqs, recordedReq{req.Method, req.URL.Path, req.URL.RawQuery, body})

	if d.failSub != "" && strings.Contains(req.URL.Path, d.failSub) {
		d.seenSub++
		if d.seenSub == d.failNth {
			return jsonResp(500, `{"message":"boom"}`), nil
		}
	}
	for sub, resp := range d.routes {
		if strings.Contains(req.URL.Path, sub) {
			return jsonResp(200, resp), nil
		}
	}
	return jsonResp(200, "{}"), nil
}

func batchInput(n int) CreateWorkoutsInput {
	items := make(WorkoutSpecInputs, n)
	for i := range items {
		items[i] = WorkoutSpecInput{
			Type: "strength", Name: "w",
			Steps: StepInputs{{Exercise: "BARBELL_BICEPS_CURL", Reps: 5}},
		}
	}
	return CreateWorkoutsInput{Workouts: items}
}

// TestWorkoutSpecInputsUnmarshal accepts the batch natively, as a stringified
// array, and with a nested stringified steps array.
func TestWorkoutSpecInputsUnmarshal(t *testing.T) {
	native := `[{"type":"strength","name":"a","steps":[{"exercise":"x","reps":"5"}]}]`
	cases := map[string]string{
		"native":          native,
		"stringified":     mustMarshalString(t, native),
		"nested stringed": `[{"type":"strength","name":"a","steps":"[{\"exercise\":\"x\",\"reps\":5}]"}]`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			var got WorkoutSpecInputs
			if err := json.Unmarshal([]byte(in), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(got) != 1 || got[0].Name != "a" || len(got[0].Steps) != 1 || got[0].Steps[0].Reps != 5 {
				t.Fatalf("bad decode: %+v", got)
			}
		})
	}
}

func mustMarshalString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestCreateWorkoutsSchema_Widened asserts the workouts array (and its nested
// step arrays) also accept a stringified form.
func TestCreateWorkoutsSchema_Widened(t *testing.T) {
	schema := createWorkoutsInputSchema()
	w, ok := schema.Properties["workouts"]
	if !ok {
		t.Fatal("no workouts property")
	}
	if len(w.Types) == 0 || w.Types[0] != "array" || w.Types[1] != "string" {
		t.Fatalf("workouts not widened: %v", w.Types)
	}
	steps, ok := w.Items.Properties["steps"]
	if !ok {
		t.Fatal("no nested steps property")
	}
	if len(steps.Types) == 0 || steps.Types[1] != "string" {
		t.Fatalf("nested steps not widened: %v", steps.Types)
	}
}

// TestCreateWorkouts_ValidateAllFirst asserts a bad item anywhere in the batch
// aborts before any HTTP call.
func TestCreateWorkouts_ValidateAllFirst(t *testing.T) {
	d := &recordDoer{t: t, failOnAny: true}
	svc := testService(t, d)

	in := batchInput(2)
	in.Workouts[1].Steps = StepInputs{{Exercise: "NOT_A_REAL_EXERCISE_XYZ", Reps: 5}}
	_, _, err := handleCreateWorkouts(svc)(context.Background(), nil, in)
	if err == nil {
		t.Fatal("expected build error")
	}
	if !strings.Contains(err.Error(), "workout 2") || !strings.Contains(err.Error(), "nothing was created") {
		t.Fatalf("error should name the item and promise no writes: %v", err)
	}
}

// TestCreateWorkouts_TooMany rejects an oversized batch before any HTTP call.
func TestCreateWorkouts_TooMany(t *testing.T) {
	d := &recordDoer{t: t, failOnAny: true}
	svc := testService(t, d)
	if _, _, err := handleCreateWorkouts(svc)(context.Background(), nil, batchInput(maxBatchWorkouts+1)); err == nil {
		t.Fatal("expected too-many error")
	}
}

// TestCreateWorkouts_DryRun returns a compact preview with no HTTP.
func TestCreateWorkouts_DryRun(t *testing.T) {
	d := &recordDoer{t: t, failOnAny: true}
	svc := testService(t, d)
	in := batchInput(2)
	in.DryRun = true
	res, _, err := handleCreateWorkouts(svc)(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("dry_run: %v", err)
	}
	assertText(t, res, "dry_run")
	assertText(t, res, `"steps":1`)
}

// TestCreateWorkouts_ContinueAndReport asserts a mid-batch create failure does
// not stop later items and is reported per-item.
func TestCreateWorkouts_ContinueAndReport(t *testing.T) {
	d := &scriptedDoer{t: t,
		routes:  map[string]string{"/workout-service/workout": `{"workoutId":987}`},
		failSub: "/workout-service/workout", failNth: 2,
	}
	svc := testService(t, d)
	res, _, err := handleCreateWorkouts(svc)(context.Background(), nil, batchInput(3))
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	assertText(t, res, `"created":2`)
	assertText(t, res, `"failed":1`)
	assertText(t, res, `"status":"failed"`)
	var creates int
	for _, r := range d.reqs {
		if r.method == http.MethodPost && strings.HasSuffix(r.path, "/workout-service/workout") {
			creates++
		}
	}
	if creates != 3 {
		t.Fatalf("want 3 create attempts, got %d", creates)
	}
}

// TestCreateWorkouts_CreatedUnscheduled surfaces the id of a workout whose
// create succeeded but whose scheduling failed.
func TestCreateWorkouts_CreatedUnscheduled(t *testing.T) {
	d := &scriptedDoer{t: t,
		routes:  map[string]string{"/workout-service/workout": `{"workoutId":987}`},
		failSub: "/workout-service/schedule", failNth: 1,
	}
	svc := testService(t, d)
	in := batchInput(1)
	in.Workouts[0].ScheduleDate = "2031-01-02"
	res, _, err := handleCreateWorkouts(svc)(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	assertText(t, res, `"status":"created_unscheduled"`)
	assertText(t, res, `"workout_id":"987"`)
}

// TestCompactCreateResult extracts the compact summary fields.
func TestCompactCreateResult(t *testing.T) {
	created := []byte(`{"workoutId":987,"workoutName":"x","sportType":{"sportTypeKey":"strength_training"},"schedule":{"workoutScheduleId":555},"workoutSegments":[{"big":"blob"}]}`)
	got, err := compactCreateResult(created, "2031-01-02")
	if err != nil {
		t.Fatalf("compactCreateResult: %v", err)
	}
	if got["workout_id"] != "987" || got["name"] != "x" || got["sport"] != "strength_training" ||
		got["scheduled"] != true || got["schedule_date"] != "2031-01-02" {
		t.Fatalf("bad compact result: %v", got)
	}
	if _, ok := got["workoutSegments"]; ok {
		t.Fatal("compact result should not echo the step blob")
	}
}

// TestCatalogSearch covers hit, limit, miss, and the empty-query error.
func TestCatalogSearch(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	hits, err := cat.Search("squat", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 || len(hits) > 5 {
		t.Fatalf("want 1-5 squat hits, got %d", len(hits))
	}
	for _, h := range hits {
		if h.Exercise == "" || h.Category == "" {
			t.Fatalf("hit missing keys: %+v", h)
		}
	}
	if miss, err := cat.Search("zzzznotanexercise", 5); err != nil || len(miss) != 0 {
		t.Fatalf("miss should be empty, err nil: %v %v", miss, err)
	}
	if _, err := cat.Search("   ", 5); err == nil {
		t.Fatal("empty query should error")
	}
}

// TestUnscheduleWorkout_Shaping asserts the DELETE schedule path.
func TestUnscheduleWorkout_Shaping(t *testing.T) {
	d := &recordDoer{t: t, routes: map[string]string{}}
	c := NewClientWithDoer(d)
	if _, err := c.UnscheduleWorkout(context.Background(), "tok", "555"); err != nil {
		t.Fatalf("UnscheduleWorkout: %v", err)
	}
	got := d.last()
	if got.method != http.MethodDelete || got.path != "/workout-service/schedule/555" {
		t.Fatalf("bad request: %s %s", got.method, got.path)
	}
}

// TestRenameWorkout asserts read-modify-write: the PUT carries the full body
// (nothing blanked) with only the name changed, and the rename is verified.
func TestRenameWorkout(t *testing.T) {
	d := &recordDoer{t: t, routes: map[string]string{
		"/workout-service/workout/9": `{"workoutId":9,"workoutName":"New","steps":[{"keep":"me"}]}`,
	}}
	svc := testService(t, d)
	if _, err := svc.RenameWorkout(context.Background(), "9", "New"); err != nil {
		t.Fatalf("RenameWorkout: %v", err)
	}
	var put *recordedReq
	for i := range d.reqs {
		if d.reqs[i].method == http.MethodPut {
			put = &d.reqs[i]
		}
	}
	if put == nil {
		t.Fatal("no PUT recorded")
	}
	if put.path != "/workout-service/workout/9" {
		t.Fatalf("bad PUT path: %s", put.path)
	}
	if !strings.Contains(put.body, `"keep":"me"`) || !strings.Contains(put.body, `"workoutName":"New"`) {
		t.Fatalf("PUT body must be the full modified workout: %s", put.body)
	}
}

// TestUnscheduleGating asserts confirm/dry_run short-circuit before any HTTP.
func TestUnscheduleGating(t *testing.T) {
	d := &recordDoer{t: t, failOnAny: true}
	svc := testService(t, d)
	res, _, err := handleUnscheduleWorkout(svc)(context.Background(), nil,
		UnscheduleWorkoutInput{ScheduleID: "555"})
	if err != nil {
		t.Fatalf("unconfirmed unschedule: %v", err)
	}
	assertText(t, res, "confirm")

	res, _, err = handleRenameWorkout(svc)(context.Background(), nil,
		RenameWorkoutInput{WorkoutID: "9", Name: "x", DryRun: true})
	if err != nil {
		t.Fatalf("rename dry_run: %v", err)
	}
	assertText(t, res, "dry_run")
}
