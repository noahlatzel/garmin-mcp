package garmin

import (
	"bytes"
	"encoding/json"
	"testing"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	return c
}

func TestCatalogResolve(t *testing.T) {
	c := testCatalog(t)
	if c.Len() < 1000 {
		t.Fatalf("catalog smaller than expected: %d entries", c.Len())
	}

	// By display name (case-insensitive), by full key, and by bare exercise key
	// should all reach the same entry.
	ex, err := c.Resolve("BARBELL_BICEPS_CURL")
	if err != nil {
		t.Fatalf("resolve by exercise key: %v", err)
	}
	if ex.Category == "" || ex.Exercise != "BARBELL_BICEPS_CURL" {
		t.Fatalf("unexpected entry: %+v", ex)
	}
	byName, err := c.Resolve(ex.Name)
	if err != nil {
		t.Fatalf("resolve by display name %q: %v", ex.Name, err)
	}
	if byName != ex {
		t.Fatalf("name vs key mismatch: %+v vs %+v", byName, ex)
	}

	if _, err := c.Resolve("definitely not a real lift"); err == nil {
		t.Fatal("expected error for unknown exercise")
	}
}

// TestBuildWorkout_WorkedExample reproduces the harvested session (SCHEMA.md):
// warmup INDOOR_ROW (time 300s) then 3 sets of BARBELL_BICEPS_CURL (12 reps,
// 10 kg) with lap-button rests between sets. Sets are expanded (no repeat block).
func TestBuildWorkout_WorkedExample(t *testing.T) {
	c := testCatalog(t)
	weight := 10.0
	w, err := c.BuildWorkout("gaggle test", []StepSpec{
		{Exercise: "INDOOR_ROW", Warmup: true, DurationSecs: 300},
		{Exercise: "BARBELL_BICEPS_CURL", Sets: 3, Reps: 12, WeightKg: &weight},
	})
	if err != nil {
		t.Fatalf("BuildWorkout: %v", err)
	}

	steps := w.WorkoutSegments[0].WorkoutSteps
	// warmup + (curl, rest, curl, rest, curl) = 6 steps, no trailing rest.
	if len(steps) != 6 {
		t.Fatalf("expected 6 expanded steps, got %d", len(steps))
	}
	for i, s := range steps {
		if s.Type != "ExecutableStepDTO" {
			t.Fatalf("step %d not executable: %s", i, s.Type)
		}
		if s.StepOrder != i+1 {
			t.Fatalf("step %d has stepOrder %d", i, s.StepOrder)
		}
	}

	warmup := steps[0]
	if warmup.StepType.StepTypeKey != "warmup" || warmup.EndCondition.ConditionTypeKey != "time" || warmup.EndConditionValue != 300 {
		t.Fatalf("bad warmup step: %+v", warmup)
	}

	curl := steps[1]
	if curl.StepType.StepTypeKey != "interval" || curl.EndCondition.ConditionTypeKey != "reps" || curl.EndConditionValue != 12 {
		t.Fatalf("bad curl step: %+v", curl)
	}
	if curl.WeightValue == nil || *curl.WeightValue != 10 || curl.WeightUnit == nil || curl.WeightUnit.UnitKey != "kilogram" {
		t.Fatalf("bad curl weight: %+v", curl)
	}
	if curl.ExerciseName == nil || *curl.ExerciseName != "BARBELL_BICEPS_CURL" {
		t.Fatalf("bad curl exercise: %+v", curl)
	}

	rest := steps[2]
	if rest.StepType.StepTypeKey != "rest" || rest.Category != nil || rest.WeightValue != nil {
		t.Fatalf("bad rest step: %+v", rest)
	}

	// Round-trip: marshals and the rest step's nullable fields serialize as null.
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(b, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if envelope["sportType"].(map[string]any)["sportTypeKey"] != "strength_training" {
		t.Fatalf("bad sportType in JSON: %s", b)
	}
}

// TestBuildWorkout_NotesSeparateFromRepsWeight: a coaching
// cue must travel to Garmin in the step description without clobbering (or being
// clobbered by) the rep target or weight, and it must apply to every expanded
// set. Steps without notes must omit the field entirely (no empty string, no
// null).
func TestBuildWorkout_NotesSeparateFromRepsWeight(t *testing.T) {
	c := testCatalog(t)
	weight := 60.0
	w, err := c.BuildWorkout("gaggle notes test", []StepSpec{
		{Exercise: "BARBELL_BACK_SQUAT", Sets: 2, Reps: 5, WeightKg: &weight, Notes: "find load 2-3 in tank"},
		{Exercise: "STANDING_CALF_RAISE", Reps: 15},
	})
	if err != nil {
		t.Fatalf("BuildWorkout: %v", err)
	}

	steps := w.WorkoutSegments[0].WorkoutSteps
	// squat, rest, squat, calf = 4 steps (rest only between the two squat sets).
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}

	for _, i := range []int{0, 2} {
		squat := steps[i]
		if squat.EndCondition.ConditionTypeKey != "reps" || squat.EndConditionValue != 5 {
			t.Fatalf("squat set %d lost its rep target: %+v", i, squat)
		}
		if squat.WeightValue == nil || *squat.WeightValue != 60 {
			t.Fatalf("squat set %d lost its weight: %+v", i, squat)
		}
		if squat.Description == nil || *squat.Description != "find load 2-3 in tank" {
			t.Fatalf("squat set %d lost its notes: %+v", i, squat)
		}
	}

	// The calf step carried no note: the field must be omitted from JSON.
	calf := steps[3]
	if calf.Description != nil {
		t.Fatalf("calf step should have no description, got %q", *calf.Description)
	}
	b, err := json.Marshal(calf)
	if err != nil {
		t.Fatalf("marshal calf: %v", err)
	}
	if bytes.Contains(b, []byte("description")) {
		t.Fatalf("empty notes must omit description key, got %s", b)
	}
}

// TestBuildWorkout_RestBetweenSets pins the tri-state: unset (nil) and explicit
// true insert a rest between sets; explicit false expands the sets
// back-to-back. A trailing rest is never added.
func TestBuildWorkout_RestBetweenSets(t *testing.T) {
	c := testCatalog(t)
	on, off := true, false
	cases := []struct {
		name      string
		rest      *bool
		wantSteps int // 3 sets -> 5 with rests, 3 without
	}{
		{"default nil", nil, 5},
		{"explicit true", &on, 5},
		{"explicit false", &off, 3},
	}
	for _, tc := range cases {
		w, err := c.BuildWorkout("rest test", []StepSpec{
			{Exercise: "BARBELL_BACK_SQUAT", Sets: 3, Reps: 5, RestBetweenSets: tc.rest},
		})
		if err != nil {
			t.Fatalf("%s: BuildWorkout: %v", tc.name, err)
		}
		steps := w.WorkoutSegments[0].WorkoutSteps
		if len(steps) != tc.wantSteps {
			t.Fatalf("%s: got %d steps, want %d", tc.name, len(steps), tc.wantSteps)
		}
		if last := steps[len(steps)-1]; last.StepType.StepTypeKey == "rest" {
			t.Fatalf("%s: trailing rest step must never be added", tc.name)
		}
	}
}

func TestBuildWorkout_Rejects(t *testing.T) {
	c := testCatalog(t)
	if _, err := c.BuildWorkout("", []StepSpec{{Exercise: "INDOOR_ROW"}}); err == nil {
		t.Fatal("expected error for empty name")
	}
	if _, err := c.BuildWorkout("x", nil); err == nil {
		t.Fatal("expected error for no steps")
	}
	if _, err := c.BuildWorkout("x", []StepSpec{{Exercise: "not real"}}); err == nil {
		t.Fatal("expected error for unknown exercise")
	}
}
