package garmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// VerifyLive drives the new connectapi tools end-to-end against the real Garmin
// account behind the token store, writing human-readable progress to out. It
// proves the paths added in api.go answer live (the unit tests only assert
// request shaping). It creates a THROWAWAY workout on a far-future date, walks it
// through list/get/schedule/calendar, then deletes it — never touching real data.
//
// It is invoked by `garmin verify` on the box; it makes real API calls, so it is
// never run from the MCP server path or unit tests.
func (s *Service) VerifyLive(ctx context.Context, out io.Writer) error {
	now := time.Now()
	schedDate := now.AddDate(5, 0, 0).Format("2006-01-02") // far-future, disposable
	schedYear, schedMonth := parseYearMonth(schedDate)
	wellnessDate := now.AddDate(0, 0, -1).Format("2006-01-02")

	logf := func(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

	logf("== garmin live verify ==")
	logf("schedule date: %s  wellness date: %s\n", schedDate, wellnessDate)

	// [1] create throwaway. The step carries a distinctive Notes cue so [3] can
	// confirm the coaching-cue -> Garmin step description mapping round-trips
	// (the field name was not in the harvested schema).
	const notesCue = "gaggle-verify cue: load 2-3 in tank"
	created, err := s.CreateStrengthWorkout(ctx, "gaggle-verify DELETE ME",
		[]StepSpec{{Exercise: "BARBELL_BACK_SQUAT", Sets: 2, Reps: 5, WeightKg: ptr(60), Notes: notesCue}}, "")
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	workoutID, err := workoutIDFromCreate(created)
	if err != nil {
		return fmt.Errorf("parsing created workoutId: %w", err)
	}
	logf("[1/7] created workoutId=%s", workoutID)

	// Best-effort cleanup so a mid-run failure never leaves the throwaway behind.
	deleted := false
	defer func() {
		if !deleted {
			if _, derr := s.DeleteWorkout(ctx, workoutID); derr != nil {
				logf("  ⚠ cleanup: could not delete %s: %v", workoutID, derr)
			} else {
				logf("  cleanup: deleted leftover workout %s", workoutID)
			}
		}
	}()

	// [2] list — confirm appears
	list, err := s.ListWorkouts(ctx, 50)
	if err != nil {
		return fmt.Errorf("list_workouts: %w", err)
	}
	if !containsWorkoutID(list, workoutID) {
		return fmt.Errorf("created workout %s not found in list_workouts", workoutID)
	}
	logf("[2/7] list_workouts ✓ found")

	// [3] get
	got, err := s.GetWorkout(ctx, workoutID)
	if err != nil {
		return fmt.Errorf("get_workout: %w", err)
	}
	if !containsWorkoutID(got, workoutID) {
		return fmt.Errorf("get_workout did not return workoutId %s", workoutID)
	}
	// Confirm the step notes survived the round-trip. Warn rather
	// than fail: if Garmin stores cues under a different key, the field name needs
	// revisiting but the rest of the tool is still sound.
	if bytes.Contains(got, []byte(notesCue)) {
		logf("[3/7] get_workout ✓ (step notes/description round-tripped)")
	} else {
		logf("[3/7] get_workout ✓ ⚠ step notes NOT echoed back — Garmin may use a different field than 'description'")
	}

	// [4] schedule far-future
	if _, err := s.ScheduleWorkout(ctx, workoutID, schedDate); err != nil {
		return fmt.Errorf("schedule_workout: %w", err)
	}
	logf("[4/7] schedule_workout ✓ (%s)", schedDate)

	// [5] calendar — confirm shows (warn, don't fail: response shape varies)
	cal, err := s.GetCalendar(ctx, schedYear, schedMonth-1) // service takes 0-indexed month
	if err != nil {
		return fmt.Errorf("get_calendar: %w", err)
	}
	if containsWorkoutID(cal, workoutID) {
		logf("[5/7] get_calendar ✓ appears")
	} else {
		logf("[5/7] get_calendar ⚠ not found — inspect calendarItems shape")
	}

	// [6] delete + confirm gone
	if _, err := s.DeleteWorkout(ctx, workoutID); err != nil {
		return fmt.Errorf("delete_workout: %w", err)
	}
	deleted = true
	list2, err := s.ListWorkouts(ctx, 50)
	if err != nil {
		return fmt.Errorf("list_workouts (post-delete): %w", err)
	}
	if containsWorkoutID(list2, workoutID) {
		return fmt.Errorf("workout %s still present after delete", workoutID)
	}
	logf("[6/7] delete_workout ✓ gone")

	// [7] wellness reads — 204/empty is "no data", not a failure
	logf("[7/7] wellness reads for %s (empty = no data that day):", wellnessDate)
	type read struct {
		name string
		call func() ([]byte, error)
	}
	reads := []read{
		{"get_daily_summary", func() ([]byte, error) { return s.GetDailySummary(ctx, wellnessDate) }},
		{"get_sleep", func() ([]byte, error) { return s.GetSleep(ctx, wellnessDate) }},
		{"get_hrv", func() ([]byte, error) { return s.GetHRV(ctx, wellnessDate) }},
		{"get_stress", func() ([]byte, error) { return s.GetStress(ctx, wellnessDate) }},
		{"get_body_battery", func() ([]byte, error) { return s.GetBodyBattery(ctx, wellnessDate, wellnessDate) }},
	}
	var failed int
	for _, r := range reads {
		body, err := r.call()
		if err != nil {
			logf("  ✗ %s failed — check path/param: %v", r.name, err)
			failed++
			continue
		}
		logf("  ✓ %s ok (%d bytes)", r.name, len(body))
	}
	if failed > 0 {
		return fmt.Errorf("%d wellness read(s) failed; fix the path(s) in api.go", failed)
	}

	logf("\n== done. Throwaway workout %s created, verified, and deleted. ==", workoutID)
	return nil
}

// VerifySwimLive round-trips the swim builder against the real Garmin account:
// it creates a THROWAWAY pool-swim workout exercising the DRAFT swim enums
// (stroke, drill, equipment, pace target, pool length), asserts each field
// echoes back via get_workout, then deletes it. Run by hand via
// `garmin verify swim`; never from the MCP server path or unit tests.
func (s *Service) VerifySwimLive(ctx context.Context, out io.Writer) error {
	logf := func(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }
	logf("== garmin swim live verify ==")

	const notesCue = "gaggle-verify swim cue: bilateral breathing"
	specs := []SwimStepSpec{
		{DistanceMeters: 200, Stroke: "free", Warmup: true},
		{DistanceMeters: 100, Stroke: "free", Repeats: 4, RestSecs: 20,
			Target: TargetSpec{Kind: TargetPace, Low: 100.0 / 105, High: 100.0 / 105}, Notes: notesCue},
		{DistanceMeters: 100, Drill: "kick", Equipment: "kickboard"},
		{DistanceMeters: 100, Stroke: "backstroke", Cooldown: true},
	}

	created, err := s.CreateSwimWorkout(ctx, "gaggle-verify swim DELETE ME", 25, specs, "")
	if err != nil {
		return fmt.Errorf("create_swim_workout: %w", err)
	}
	workoutID, err := workoutIDFromCreate(created)
	if err != nil {
		return fmt.Errorf("parsing created workoutId: %w", err)
	}
	logf("[1/3] created workoutId=%s", workoutID)

	deleted := false
	defer func() {
		if !deleted {
			if _, derr := s.DeleteWorkout(ctx, workoutID); derr != nil {
				logf("  ⚠ cleanup: could not delete %s: %v", workoutID, derr)
			} else {
				logf("  cleanup: deleted leftover workout %s", workoutID)
			}
		}
	}()

	got, err := s.GetWorkout(ctx, workoutID)
	if err != nil {
		return fmt.Errorf("get_workout: %w", err)
	}

	// Each check pins one DRAFT enum family; a failure means that family's
	// ids/keys need re-harvesting (data/SCHEMA.md swim section).
	checks := []struct{ name, needle string }{
		{"sportType swimming", `"sportTypeKey":"swimming"`},
		{"poolLength 25", `"poolLength":25`},
		{"stroke free", `"strokeTypeKey":"free"`},
		{"stroke backstroke", `"strokeTypeKey":"backstroke"`},
		{"drill kick", `"drillTypeKey":"kick"`},
		{"equipment kickboard", `"equipmentTypeKey":"kickboard"`},
		{"pace target (secondary)", `"workoutTargetTypeKey":"pace.zone"`},
		{"fixed rest", `"conditionTypeKey":"fixed.rest"`},
		{"step notes", notesCue},
	}
	failed := 0
	compact := compactJSON(got)
	for _, c := range checks {
		if bytes.Contains(compact, []byte(c.needle)) {
			logf("[2/3] ✓ %s round-tripped", c.name)
		} else {
			logf("[2/3] ✗ %s NOT echoed back (looked for %s)", c.name, c.needle)
			failed++
		}
	}

	if _, err := s.DeleteWorkout(ctx, workoutID); err != nil {
		return fmt.Errorf("delete_workout: %w", err)
	}
	deleted = true
	logf("[3/3] deleted throwaway workout %s", workoutID)

	if failed > 0 {
		return fmt.Errorf("%d swim field(s) did not round-trip — re-harvest the DRAFT enums in data/SCHEMA.md", failed)
	}
	logf("\n== done. Swim schema round-trip verified. ==")
	return nil
}

// compactJSON re-encodes a JSON body without whitespace so substring checks are
// formatting-independent; on parse failure it returns the input unchanged.
func compactJSON(body []byte) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, body); err != nil {
		return body
	}
	return buf.Bytes()
}

// containsWorkoutID reports whether the given workout id appears anywhere in the
// JSON body (handles both flat lists and nested calendarItems).
func containsWorkoutID(body []byte, id string) bool {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return false
	}
	return walkForID(v, id)
}

func walkForID(v any, id string) bool {
	switch t := v.(type) {
	case map[string]any:
		if w, ok := t["workoutId"]; ok && numEqual(w, id) {
			return true
		}
		for _, child := range t {
			if walkForID(child, id) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if walkForID(child, id) {
				return true
			}
		}
	}
	return false
}

// numEqual compares a JSON-decoded value (float64 or string) to a decimal id.
func numEqual(v any, id string) bool {
	switch n := v.(type) {
	case float64:
		return fmt.Sprintf("%.0f", n) == id
	case string:
		return n == id
	case json.Number:
		return n.String() == id
	}
	return false
}

func parseYearMonth(date string) (year, month int) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, 0
	}
	return t.Year(), int(t.Month())
}

func ptr(f float64) *float64 { return &f }
