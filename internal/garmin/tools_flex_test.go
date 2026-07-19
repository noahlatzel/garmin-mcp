package garmin

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
)

// Regression: some MCP clients stringify scalar arguments — booleans arrive
// as "true", numbers as "25". The garmin inputs and published schemas must
// tolerate both encodings.

func TestGarminInputsTolerateStringifiedScalars(t *testing.T) {
	var del DeleteWorkoutInput
	if err := json.Unmarshal([]byte(`{"workout_id":"1","confirm":"true","dry_run":"false"}`), &del); err != nil {
		t.Fatalf("DeleteWorkoutInput: %v", err)
	}
	if !del.Confirm.Bool() || del.DryRun.Bool() {
		t.Errorf("DeleteWorkoutInput = confirm %v, dry_run %v; want true, false", del.Confirm, del.DryRun)
	}

	var swim CreateSwimWorkoutInput
	swimJSON := `{"name":"n","pool_length_m":"33.3","dry_run":"true",
		"steps":[{"distance_m":"100","repeats":"4","rest_secs":"20","warmup":"false","target_hr_zone":"3"}]}`
	if err := json.Unmarshal([]byte(swimJSON), &swim); err != nil {
		t.Fatalf("CreateSwimWorkoutInput: %v", err)
	}
	if swim.PoolLengthM.Float() != 33.3 || !swim.DryRun.Bool() {
		t.Errorf("swim top-level = pool %v dry_run %v", swim.PoolLengthM, swim.DryRun)
	}
	s := swim.Steps[0]
	if s.DistanceMeters.Int() != 100 || s.Repeats.Int() != 4 || s.RestSecs.Int() != 20 || s.Warmup.Bool() || s.HRZone.Int() != 3 {
		t.Errorf("swim step = %+v", s)
	}

	var strength CreateStrengthWorkoutInput
	strengthJSON := `{"name":"n","dry_run":"true",
		"steps":[{"exercise":"x","sets":"3","reps":"12","weight_kg":"60.5","rest_between_sets":"true"}]}`
	if err := json.Unmarshal([]byte(strengthJSON), &strength); err != nil {
		t.Fatalf("CreateStrengthWorkoutInput: %v", err)
	}
	st := strength.Steps[0]
	if st.Sets.Int() != 3 || st.Reps.Int() != 12 || st.WeightKg.Float() != 60.5 || !strength.DryRun.Bool() {
		t.Errorf("strength step = %+v dry_run %v", st, strength.DryRun)
	}
	if st.RestBetweenSets == nil || !st.RestBetweenSets.Bool() {
		t.Errorf("rest_between_sets = %v, want explicit true", st.RestBetweenSets)
	}

	var cal CalendarInput
	if err := json.Unmarshal([]byte(`{"year":"2026","month":"7"}`), &cal); err != nil {
		t.Fatalf("CalendarInput: %v", err)
	}
	if cal.Year.Int() != 2026 || cal.Month.Int() != 7 {
		t.Errorf("calendar = %+v", cal)
	}
}

func TestGarminSchemasWidenStringifiedScalars(t *testing.T) {
	assertWidened := func(t *testing.T, schema *jsonschema.Schema, kind string, props ...string) {
		t.Helper()
		for _, p := range props {
			prop := schema.Properties[p]
			if prop == nil {
				t.Fatalf("property %q missing", p)
			}
			if prop.Type != "" || len(prop.Types) != 3 || prop.Types[0] != kind || prop.Types[1] != "string" || prop.Types[2] != "null" {
				t.Errorf("%s: Type=%q Types=%v, want [%s string null]", p, prop.Type, prop.Types, kind)
			}
		}
	}

	strength := strengthWorkoutInputSchema()
	assertWidened(t, strength, "boolean", "dry_run")
	items := strength.Properties["steps"].Items
	if items == nil {
		t.Fatal("strength steps items schema missing")
	}
	assertWidened(t, items, "integer", "sets", "reps", "duration_secs")
	assertWidened(t, items, "number", "weight_kg")
	assertWidened(t, items, "boolean", "warmup", "rest_between_sets")

	swim := swimWorkoutInputSchema()
	assertWidened(t, swim, "boolean", "dry_run")
	assertWidened(t, swim, "number", "pool_length_m")
	swimItems := swim.Properties["steps"].Items
	if swimItems == nil {
		t.Fatal("swim steps items schema missing")
	}
	assertWidened(t, swimItems, "integer", "distance_m", "repeats", "rest_secs", "target_hr_zone")
	assertWidened(t, swimItems, "boolean", "warmup", "cooldown")

	assertWidened(t, mcputil.InputSchema[DeleteWorkoutInput]("delete_workout", "confirm", "dry_run"), "boolean", "confirm", "dry_run")
	assertWidened(t, mcputil.InputSchema[CalendarInput]("get_calendar", "year", "month"), "integer", "year", "month")
	assertWidened(t, mcputil.InputSchema[ListWorkoutsInput]("list_workouts", "limit"), "integer", "limit")

	// get_workout takes only the id; the write gates belong to delete_workout.
	get := mcputil.InputSchema[GetWorkoutInput]("get_workout")
	for _, p := range []string{"confirm", "dry_run"} {
		if _, ok := get.Properties[p]; ok {
			t.Errorf("get_workout schema must not advertise %q", p)
		}
	}
}
