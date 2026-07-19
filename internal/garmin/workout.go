package garmin

import (
	"fmt"
	"strings"
)

// Garmin strength enum values, harvested from the live builder (data/SCHEMA.md).
const (
	sportStrengthID  = 5
	sportStrengthKey = "strength_training"

	stepTypeWarmupID  = 1
	stepTypeWarmupKey = "warmup"
	stepTypeInterval  = 3
	stepTypeIntervKey = "interval"
	stepTypeRestID    = 5
	stepTypeRestKey   = "rest"

	condLapButtonID  = 1
	condLapButtonKey = "lap.button"
	condTimeID       = 2
	condTimeKey      = "time"
	condRepsID       = 10
	condRepsKey      = "reps"

	targetNoTargetID  = 1
	targetNoTargetKey = "no.target"
)

// Swim enum values, harvested 2026-07-09 from a real pool-swim save in the
// Garmin Connect web builder (see data/SCHEMA.md "Swim (pool)").
const (
	sportSwimID  = 4
	sportSwimKey = "swimming"

	stepTypeCooldownID  = 2
	stepTypeCooldownKey = "cooldown"
	// "main" is the working-set step type for swim; interval (3) is what the
	// builder uses for drill steps.
	stepTypeMainID  = 8
	stepTypeMainKey = "main"

	condDistanceID  = 3
	condDistanceKey = "distance"
	condFixedRestID = 8
	condFixedRest   = "fixed.rest"
)

// strokeType, equipmentType and drillType are the swim step enums (each Garmin's
// {id,key} shape with its own field names). Id 0 / null key means "any"/none.

type strokeType struct {
	StrokeTypeID  int     `json:"strokeTypeId"`
	StrokeTypeKey *string `json:"strokeTypeKey"`
	DisplayOrder  int     `json:"displayOrder"`
}

// equipmentType uses pointer fields because "no equipment" serializes as all
// nulls (harvested), not id 0.
type equipmentType struct {
	EquipmentTypeID  *int    `json:"equipmentTypeId"`
	EquipmentTypeKey *string `json:"equipmentTypeKey"`
	DisplayOrder     *int    `json:"displayOrder"`
}

type drillType struct {
	DrillTypeID  int    `json:"drillTypeId"`
	DrillTypeKey string `json:"drillTypeKey"`
	DisplayOrder int    `json:"displayOrder"`
}

// distanceUnit is the unit shape used for pool length and a distance step's
// preferredEndConditionUnit. The harvested payload sends only the key.
type distanceUnit struct {
	UnitKey string `json:"unitKey"`
}

var meter = &distanceUnit{UnitKey: "meter"}

// strokeTypes/equipmentTypes/drillTypes map the tool-level lowercase names to
// Garmin's enum values. DRAFT ids (see data/SCHEMA.md).
var (
	strokeTypes = map[string]strokeType{
		"any_stroke":   {StrokeTypeID: 1, StrokeTypeKey: strPtr("any_stroke"), DisplayOrder: 1},
		"backstroke":   {StrokeTypeID: 2, StrokeTypeKey: strPtr("backstroke"), DisplayOrder: 2},
		"breaststroke": {StrokeTypeID: 3, StrokeTypeKey: strPtr("breaststroke"), DisplayOrder: 3},
		"fly":          {StrokeTypeID: 5, StrokeTypeKey: strPtr("fly"), DisplayOrder: 5},
		"free":         {StrokeTypeID: 6, StrokeTypeKey: strPtr("free"), DisplayOrder: 6},
	}
	equipmentTypes = map[string]equipmentType{
		"fins":      {EquipmentTypeID: intPtr(1), EquipmentTypeKey: strPtr("fins"), DisplayOrder: intPtr(1)},
		"kickboard": {EquipmentTypeID: intPtr(2), EquipmentTypeKey: strPtr("kickboard"), DisplayOrder: intPtr(2)},
		"paddles":   {EquipmentTypeID: intPtr(3), EquipmentTypeKey: strPtr("paddles"), DisplayOrder: intPtr(3)},
		"pull_buoy": {EquipmentTypeID: intPtr(4), EquipmentTypeKey: strPtr("pull_buoy"), DisplayOrder: intPtr(4)},
		"snorkel":   {EquipmentTypeID: intPtr(5), EquipmentTypeKey: strPtr("snorkel"), DisplayOrder: intPtr(5)},
	}
	drillTypes = map[string]drillType{
		"kick":  {DrillTypeID: 1, DrillTypeKey: "kick", DisplayOrder: 1},
		"pull":  {DrillTypeID: 2, DrillTypeKey: "pull", DisplayOrder: 2},
		"drill": {DrillTypeID: 3, DrillTypeKey: "drill", DisplayOrder: 3},
	}
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// sportType, stepType, endCondition and targetType all wrap Garmin's {id,key}
// enum shape, but each serializes with its own field names, so each is its own
// struct.

type sportType struct {
	SportTypeID  int    `json:"sportTypeId"`
	SportTypeKey string `json:"sportTypeKey"`
	DisplayOrder int    `json:"displayOrder"`
}

type stepType struct {
	StepTypeID  int    `json:"stepTypeId"`
	StepTypeKey string `json:"stepTypeKey"`
}

type endCondition struct {
	ConditionTypeID  int    `json:"conditionTypeId"`
	ConditionTypeKey string `json:"conditionTypeKey"`
}

type targetType struct {
	WorkoutTargetTypeID  int    `json:"workoutTargetTypeId"`
	WorkoutTargetTypeKey string `json:"workoutTargetTypeKey"`
}

type weightUnit struct {
	UnitID  int     `json:"unitId"`
	UnitKey string  `json:"unitKey"`
	Factor  float64 `json:"factor"`
}

var kilogram = &weightUnit{UnitID: 8, UnitKey: "kilogram", Factor: 1000.0}

// executableStep is one ExecutableStepDTO: a single real step (one exercise, or
// a rest). gaggle rule §1.5: exactly one exercise per step, never merged.
type executableStep struct {
	Type              string       `json:"type"`
	StepOrder         int          `json:"stepOrder"`
	StepType          stepType     `json:"stepType"`
	EndCondition      endCondition `json:"endCondition"`
	EndConditionValue float64      `json:"endConditionValue"`
	Category          *string      `json:"category"`
	ExerciseName      *string      `json:"exerciseName"`
	WeightValue       *float64     `json:"weightValue"`
	WeightUnit        *weightUnit  `json:"weightUnit"`
	TargetType        targetType   `json:"targetType"`
	// Description carries a per-step coaching cue (Garmin's step "Notes" field).
	// It is independent of reps/weight and is omitted when empty. The field name
	// was not in the harvested schema (data/SCHEMA.md) — round-trip verify against
	// the live workout-service (create then get_workout) before trusting it.
	Description *string `json:"description,omitempty"`

	// Multi-sport fields, all omitted when unset so strength payloads are
	// unchanged. Zone/range targets: targetValueOne/Two carry a range in the
	// target's canonical unit (pace/speed m/s, HR bpm), zoneNumber a numbered
	// zone. Swim steps carry stroke/equipment (id 0 = any) and optional drill;
	// distance steps pin the display unit via preferredEndConditionUnit.
	TargetValueOne            *float64       `json:"targetValueOne,omitempty"`
	TargetValueTwo            *float64       `json:"targetValueTwo,omitempty"`
	ZoneNumber                *int           `json:"zoneNumber,omitempty"`
	SecondaryTargetType       *targetType    `json:"secondaryTargetType,omitempty"`
	SecondaryTargetValueOne   *float64       `json:"secondaryTargetValueOne,omitempty"`
	SecondaryTargetValueTwo   *float64       `json:"secondaryTargetValueTwo,omitempty"`
	SecondaryZoneNumber       *int           `json:"secondaryZoneNumber,omitempty"`
	StrokeType                *strokeType    `json:"strokeType,omitempty"`
	EquipmentType             *equipmentType `json:"equipmentType,omitempty"`
	DrillType                 *drillType     `json:"drillType,omitempty"`
	PreferredEndConditionUnit *distanceUnit  `json:"preferredEndConditionUnit,omitempty"`
}

type segment struct {
	SegmentOrder int              `json:"segmentOrder"`
	SportType    sportType        `json:"sportType"`
	WorkoutSteps []executableStep `json:"workoutSteps"`
}

// Workout is the workout envelope POSTed to Garmin Connect. PoolLength and
// PoolLengthUnit are set only for pool-swim workouts (omitted for strength).
type Workout struct {
	WorkoutName     string        `json:"workoutName"`
	SportType       sportType     `json:"sportType"`
	WorkoutSegments []segment     `json:"workoutSegments"`
	PoolLength      *float64      `json:"poolLength,omitempty"`
	PoolLengthUnit  *distanceUnit `json:"poolLengthUnit,omitempty"`
}

// StepSpec is the caller's high-level description of one exercise in a workout.
// gaggle expands Sets into that many discrete interval steps (never an Nx repeat
// block — those collapse silently on the watch; see CLAUDE.md §5). A rest step
// (lap-button) is inserted between sets.
type StepSpec struct {
	// Exercise is a display name or key resolvable by the Catalog. Empty means a
	// pure rest step.
	Exercise string
	// Sets is the number of times to perform the exercise (>=1). Expanded into
	// discrete steps.
	Sets int
	// Reps per set. When >0 the step ends on a reps condition; otherwise it ends
	// on the lap button (manual advance).
	Reps int
	// WeightKg is the optional working weight in kilograms. nil/0 omits weight.
	WeightKg *float64
	// DurationSecs, when >0, makes a time-based step (e.g. a warmup row) ending
	// after that many seconds. Mutually informative with Reps; Reps wins if both
	// are set.
	DurationSecs int
	// Warmup marks the step as a warmup (stepType warmup) rather than interval.
	Warmup bool
	// RestBetweenSets controls the lap-button rest step inserted between sets.
	// nil defaults to true; explicit false expands the sets back-to-back. A
	// trailing rest after the final set is never added either way.
	RestBetweenSets *bool
	// Notes is an optional coaching cue (weight guidance, tempo, technique) kept
	// separate from Reps/WeightKg and mapped to the Garmin step description. Empty
	// omits it. It applies to every expanded set of the exercise.
	Notes string
}

// BuildWorkout assembles a strength Workout from a name and a sequence of step
// specs, resolving each exercise against the catalog and expanding sets into
// discrete steps. It enforces gaggle's hard rules: one exercise per step,
// repeats expanded (no RepeatGroupDTO), named exercise + reps (+ optional
// weight). It is pure: no network, no time, no global state.
func (c *Catalog) BuildWorkout(name string, specs []StepSpec) (*Workout, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("workout name is required")
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("workout %q has no steps", name)
	}

	sport := sportType{SportTypeID: sportStrengthID, SportTypeKey: sportStrengthKey, DisplayOrder: sportStrengthID}
	noTarget := targetType{WorkoutTargetTypeID: targetNoTargetID, WorkoutTargetTypeKey: targetNoTargetKey}

	var steps []executableStep
	order := 1
	addStep := func(s executableStep) {
		s.StepOrder = order
		s.TargetType = noTarget
		steps = append(steps, s)
		order++
	}

	for i, spec := range specs {
		// Pure rest step (no exercise): a single lap-button rest.
		if strings.TrimSpace(spec.Exercise) == "" {
			addStep(restStep())
			continue
		}

		ex, err := c.Resolve(spec.Exercise)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}

		sets := spec.Sets
		if sets < 1 {
			sets = 1
		}

		// Rest between sets defaults on; explicit RestBetweenSets=false skips it.
		restBetween := spec.RestBetweenSets == nil || *spec.RestBetweenSets
		for set := 0; set < sets; set++ {
			addStep(exerciseStep(ex, spec))
			// Insert a lap-button rest between sets, never after the final set.
			if restBetween && set < sets-1 {
				addStep(restStep())
			}
		}
	}

	return &Workout{
		WorkoutName: name,
		SportType:   sport,
		WorkoutSegments: []segment{{
			SegmentOrder: 1,
			SportType:    sport,
			WorkoutSteps: steps,
		}},
	}, nil
}

// exerciseStep builds one executable exercise step from a resolved catalog entry
// and the spec's reps/weight/duration/warmup flags.
func exerciseStep(ex Exercise, spec StepSpec) executableStep {
	category := ex.Category
	exerciseName := ex.Exercise

	st := stepType{StepTypeID: stepTypeInterval, StepTypeKey: stepTypeIntervKey}
	if spec.Warmup {
		st = stepType{StepTypeID: stepTypeWarmupID, StepTypeKey: stepTypeWarmupKey}
	}

	var cond endCondition
	var condValue float64
	switch {
	case spec.Reps > 0:
		cond = endCondition{ConditionTypeID: condRepsID, ConditionTypeKey: condRepsKey}
		condValue = float64(spec.Reps)
	case spec.DurationSecs > 0:
		cond = endCondition{ConditionTypeID: condTimeID, ConditionTypeKey: condTimeKey}
		condValue = float64(spec.DurationSecs)
	default:
		cond = endCondition{ConditionTypeID: condLapButtonID, ConditionTypeKey: condLapButtonKey}
	}

	step := executableStep{
		Type:              "ExecutableStepDTO",
		StepType:          st,
		EndCondition:      cond,
		EndConditionValue: condValue,
		Category:          &category,
		ExerciseName:      &exerciseName,
	}
	if spec.WeightKg != nil && *spec.WeightKg > 0 {
		w := *spec.WeightKg
		step.WeightValue = &w
		step.WeightUnit = kilogram
	}
	if notes := strings.TrimSpace(spec.Notes); notes != "" {
		step.Description = &notes
	}
	return step
}

// restStep builds a lap-button rest step (no exercise, no weight).
func restStep() executableStep {
	return executableStep{
		Type:         "ExecutableStepDTO",
		StepType:     stepType{StepTypeID: stepTypeRestID, StepTypeKey: stepTypeRestKey},
		EndCondition: endCondition{ConditionTypeID: condLapButtonID, ConditionTypeKey: condLapButtonKey},
	}
}
