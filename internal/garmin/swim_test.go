package garmin

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestParsePacePer100m(t *testing.T) {
	cases := []struct {
		in      string
		want    float64 // m/s
		wantErr bool
	}{
		{"1:40", 1.0, false}, // 100s per 100m
		{"2:05", 0.8, false}, // 125s
		{"100", 1.0, false},  // bare seconds
		{"1:75", 0, true},    // seconds >= 60
		{"", 0, true},
		{"fast", 0, true},
	}
	for _, tc := range cases {
		got, err := ParsePacePer100m(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParsePacePer100m(%q): want error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePacePer100m(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("ParsePacePer100m(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestTargetSpecValidate(t *testing.T) {
	valid := []TargetSpec{
		{},
		{Kind: TargetPace, Low: 0.8, High: 1.0},
		{Kind: TargetHeartRate, Zone: 3},
		{Kind: TargetHeartRate, Low: 130, High: 150},
	}
	for _, ts := range valid {
		if err := ts.Validate(sportSwimKey); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", ts, err)
		}
	}
	invalid := []TargetSpec{
		{Kind: TargetPower, Zone: 3},                   // power not allowed for swim
		{Kind: TargetPace},                             // no zone, no range
		{Kind: TargetPace, Zone: 2, Low: 0.8, High: 1}, // both
		{Kind: TargetHeartRate, Zone: 9},               // zone out of range
		{Kind: TargetPace, Low: 1.2, High: 0.8},        // inverted range
		{Zone: 3},                                      // values without kind
	}
	for _, ts := range invalid {
		if err := ts.Validate(sportSwimKey); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", ts)
		}
	}
}

// TestBuildSwimWorkout_WorkedExample pins the JSON shape of a representative
// pool session: warmup, a repeated paced set with timed rests, a drill with
// equipment, and a cooldown.
func TestBuildSwimWorkout_WorkedExample(t *testing.T) {
	specs := []SwimStepSpec{
		{DistanceMeters: 200, Stroke: "free", Warmup: true},
		{DistanceMeters: 100, Stroke: "free", Repeats: 3, RestSecs: 20,
			Target: TargetSpec{Kind: TargetPace, Low: 0.8, High: 0.8}, Notes: "strong catch"},
		{DistanceMeters: 100, Drill: "kick", Equipment: "kickboard"},
		{DistanceMeters: 100, Stroke: "backstroke", Cooldown: true},
	}
	w, err := BuildSwimWorkout("Pool Session", 0, specs)
	if err != nil {
		t.Fatalf("BuildSwimWorkout: %v", err)
	}

	if w.SportType.SportTypeID != 4 || w.SportType.SportTypeKey != "swimming" {
		t.Errorf("sportType = %+v, want swimming/4", w.SportType)
	}
	if w.PoolLength == nil || *w.PoolLength != 25 {
		t.Errorf("poolLength = %v, want default 25", w.PoolLength)
	}
	if w.PoolLengthUnit == nil || w.PoolLengthUnit.UnitKey != "meter" {
		t.Errorf("poolLengthUnit = %+v, want meter", w.PoolLengthUnit)
	}

	steps := w.WorkoutSegments[0].WorkoutSteps
	// warmup + (3 x 100m with 2 rests between) + drill + cooldown = 1+5+1+1
	if len(steps) != 8 {
		t.Fatalf("got %d steps, want 8", len(steps))
	}
	for i, s := range steps {
		if s.StepOrder != i+1 {
			t.Errorf("step %d: stepOrder = %d", i, s.StepOrder)
		}
	}

	warmup := steps[0]
	if warmup.StepType.StepTypeKey != "warmup" || warmup.EndCondition.ConditionTypeKey != "distance" || warmup.EndConditionValue != 200 {
		t.Errorf("warmup step wrong: %+v", warmup)
	}
	if warmup.PreferredEndConditionUnit == nil || warmup.PreferredEndConditionUnit.UnitKey != "meter" {
		t.Errorf("warmup preferredEndConditionUnit = %+v, want meter", warmup.PreferredEndConditionUnit)
	}
	if warmup.StrokeType == nil || warmup.StrokeType.StrokeTypeID != 6 {
		t.Errorf("warmup stroke = %+v, want free/6", warmup.StrokeType)
	}

	// Paced set: steps 2,4,6 are 100m "main" steps with the pace as a secondary
	// single-value target (primary stays no.target); 3,5 are fixed.rest steps.
	for _, i := range []int{1, 3, 5} {
		s := steps[i]
		if s.EndConditionValue != 100 || s.StepType.StepTypeKey != "main" {
			t.Errorf("set step %d wrong: %+v", i, s)
		}
		if s.TargetType.WorkoutTargetTypeKey != "no.target" {
			t.Errorf("set step %d primary target = %+v, want no.target", i, s.TargetType)
		}
		if s.SecondaryTargetType == nil || s.SecondaryTargetType.WorkoutTargetTypeKey != "pace.zone" ||
			s.SecondaryTargetValueOne == nil || *s.SecondaryTargetValueOne != 0.8 || s.SecondaryTargetValueTwo != nil {
			t.Errorf("set step %d secondary target wrong: %+v %v", i, s.SecondaryTargetType, s.SecondaryTargetValueOne)
		}
		if s.Description == nil || *s.Description != "strong catch" {
			t.Errorf("set step %d notes = %v", i, s.Description)
		}
	}
	for _, i := range []int{2, 4} {
		s := steps[i]
		if s.StepType.StepTypeKey != "rest" || s.EndCondition.ConditionTypeKey != "fixed.rest" || s.EndConditionValue != 20 {
			t.Errorf("rest step %d wrong: %+v", i, s)
		}
	}

	drill := steps[6]
	if drill.StepType.StepTypeKey != "interval" {
		t.Errorf("drill stepType = %+v, want interval", drill.StepType)
	}
	if drill.DrillType == nil || drill.DrillType.DrillTypeKey != "kick" {
		t.Errorf("drill = %+v, want kick", drill.DrillType)
	}
	if drill.EquipmentType == nil || drill.EquipmentType.EquipmentTypeID == nil || *drill.EquipmentType.EquipmentTypeID != 2 {
		t.Errorf("equipment = %+v, want kickboard/2", drill.EquipmentType)
	}
	if drill.StrokeType != nil {
		t.Errorf("drill stroke = %+v, want omitted (no stroke chosen)", drill.StrokeType)
	}

	cooldown := steps[7]
	if cooldown.StepType.StepTypeKey != "cooldown" || cooldown.StrokeType.StrokeTypeID != 2 {
		t.Errorf("cooldown wrong: %+v", cooldown)
	}

	// The serialized JSON must never contain a repeat block, and swim steps must
	// carry no strength fields.
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "RepeatGroupDTO") {
		t.Error("workout JSON contains a RepeatGroupDTO — repeats must be expanded")
	}
	if strings.Contains(string(raw), `"weightValue":0`) || strings.Contains(string(raw), `"strokeTypeKey":""`) {
		t.Errorf("unexpected field encoding in: %s", raw)
	}
}

func TestBuildSwimWorkoutRejectsBadSpecs(t *testing.T) {
	cases := []struct {
		name  string
		pool  float64
		specs []SwimStepSpec
	}{
		{"no steps", 25, nil},
		{"both distance and duration", 25, []SwimStepSpec{{DistanceMeters: 100, DurationSecs: 60}}},
		{"unknown stroke", 25, []SwimStepSpec{{DistanceMeters: 100, Stroke: "doggy_paddle"}}},
		{"unknown drill", 25, []SwimStepSpec{{DistanceMeters: 100, Drill: "sculling"}}},
		{"unknown equipment", 25, []SwimStepSpec{{DistanceMeters: 100, Equipment: "wetsuit"}}},
		{"warmup and cooldown", 25, []SwimStepSpec{{DistanceMeters: 100, Warmup: true, Cooldown: true}}},
		{"power target", 25, []SwimStepSpec{{DistanceMeters: 100, Target: TargetSpec{Kind: TargetPower, Zone: 3}}}},
		{"rest with stroke", 25, []SwimStepSpec{{Stroke: "free"}}},
		{"absurd pool", 5, []SwimStepSpec{{DistanceMeters: 100}}},
	}
	for _, tc := range cases {
		if _, err := BuildSwimWorkout("x", tc.pool, tc.specs); err == nil {
			t.Errorf("%s: want error, got nil", tc.name)
		}
	}
}

func TestSwimStepInputsTolerateStringifiedArray(t *testing.T) {
	native := `{"name":"n","steps":[{"distance_m":100,"stroke":"free"}]}`
	stringified := `{"name":"n","steps":"[{\"distance_m\":100,\"stroke\":\"free\"}]"}`
	for _, in := range []string{native, stringified} {
		var got CreateSwimWorkoutInput
		if err := json.Unmarshal([]byte(in), &got); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		if len(got.Steps) != 1 || got.Steps[0].DistanceMeters != 100 || got.Steps[0].Stroke != "free" {
			t.Errorf("steps = %+v", got.Steps)
		}
	}

	schema := swimWorkoutInputSchema()
	steps := schema.Properties["steps"]
	if steps == nil || steps.Type != "" || len(steps.Types) != 3 {
		t.Errorf("steps schema not widened: %+v", steps)
	}
}

func TestSwimTargetSpec(t *testing.T) {
	single, err := swimTargetSpec(SwimStepInput{PacePer100m: "1:40"})
	if err != nil {
		t.Fatalf("swimTargetSpec: %v", err)
	}
	if single.Kind != TargetPace || math.Abs(single.Low-1.0) > 1e-9 || single.Low != single.High {
		t.Errorf("single pace = %+v, want 1.0 m/s low==high", single)
	}

	// Garmin swim pace targets are single-valued; ranges are rejected.
	if _, err := swimTargetSpec(SwimStepInput{PacePer100m: "1:40-2:05"}); err == nil {
		t.Error("pace range: want error")
	}

	hr, err := swimTargetSpec(SwimStepInput{HRZone: 3})
	if err != nil || hr.Kind != TargetHeartRate || hr.Zone != 3 {
		t.Errorf("hr = %+v err %v", hr, err)
	}

	if _, err := swimTargetSpec(SwimStepInput{PacePer100m: "1:40", HRZone: 2}); err == nil {
		t.Error("both targets: want error")
	}
	if _, err := swimTargetSpec(SwimStepInput{PacePer100m: "nope"}); err == nil {
		t.Error("bad pace: want error")
	}
}
