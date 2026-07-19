package garmin

import (
	"encoding/json"
	"testing"
)

// TestStepInputsUnmarshal_NativeAndStringified guards the MCP-boundary
// regression: some clients stringify the
// steps array. Both the native array and a JSON-encoded string of that array
// must decode to the identical slice.
func TestStepInputsUnmarshal_NativeAndStringified(t *testing.T) {
	nativeArray := `[
		{"duration_secs":300,"exercise":"Rowing Machine","warmup":true},
		{"exercise":"Leg Press","reps":5,"warmup":true,"weight_kg":75},
		{"exercise":"Leg Press","reps":12,"sets":2,"weight_kg":117},
		{"exercise":"Standing Calf Raise","reps":15,"sets":2}
	]`

	var fromArray StepInputs
	if err := json.Unmarshal([]byte(nativeArray), &fromArray); err != nil {
		t.Fatalf("native array: %v", err)
	}

	// A JSON string whose content is the same array (double-encoded).
	stringified, err := json.Marshal(nativeArray)
	if err != nil {
		t.Fatalf("marshal stringified: %v", err)
	}
	var fromString StepInputs
	if err := json.Unmarshal(stringified, &fromString); err != nil {
		t.Fatalf("stringified array: %v", err)
	}

	if len(fromArray) != 4 || len(fromString) != 4 {
		t.Fatalf("want 4 steps each, got native=%d string=%d", len(fromArray), len(fromString))
	}
	a, _ := json.Marshal(fromArray)
	b, _ := json.Marshal(fromString)
	if string(a) != string(b) {
		t.Fatalf("decoded slices differ:\n native=%s\n string=%s", a, b)
	}
	if fromArray[1].WeightKg != 75 || fromArray[2].Sets != 2 {
		t.Fatalf("field decode wrong: %+v", fromArray)
	}
}

// TestCreateStrengthWorkoutInput_StringifiedSteps checks the whole input struct
// decodes when steps arrives as a stringified array alongside plain-string
// siblings — the exact shape the client sends.
func TestCreateStrengthWorkoutInput_StringifiedSteps(t *testing.T) {
	payload := `{
		"name":"Full Body #5",
		"schedule_date":"2027-01-01",
		"steps":"[{\"exercise\":\"Leg Press\",\"reps\":5},{\"exercise\":\"Standing Calf Raise\",\"reps\":15}]"
	}`
	var in CreateStrengthWorkoutInput
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if in.Name != "Full Body #5" || in.ScheduleDate != "2027-01-01" {
		t.Fatalf("scalar fields wrong: %+v", in)
	}
	if len(in.Steps) != 2 || in.Steps[0].Exercise != "Leg Press" {
		t.Fatalf("steps decode wrong: %+v", in.Steps)
	}
}

// TestStrengthWorkoutInputSchema_StepsAcceptsString ensures the published schema
// advertises steps as array-or-string so the SDK's pre-handler validation admits
// a stringified array instead of rejecting the whole call.
func TestStrengthWorkoutInputSchema_StepsAcceptsString(t *testing.T) {
	schema := strengthWorkoutInputSchema()
	steps, ok := schema.Properties["steps"]
	if !ok {
		t.Fatal("steps property missing from schema")
	}
	var hasArray, hasString bool
	for _, ty := range steps.Types {
		switch ty {
		case "array":
			hasArray = true
		case "string":
			hasString = true
		}
	}
	if !hasArray || !hasString {
		t.Fatalf("steps schema must accept array and string, got %v", steps.Types)
	}
}
