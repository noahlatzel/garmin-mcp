package garmin

import (
	"fmt"
	"strings"
)

// SwimStepSpec is the caller's high-level description of one pool-swim step.
// Repeats are expanded into discrete steps (never a RepeatGroupDTO — those
// collapse silently on the watch), with a rest step between repeats when
// RestSecs > 0.
type SwimStepSpec struct {
	// DistanceMeters ends the step on distance. Exactly one of DistanceMeters
	// and DurationSecs must be set for a swim step; a step with neither is a
	// standalone rest (lap-button, or timed if RestSecs is set).
	DistanceMeters int
	// DurationSecs ends the step on time (e.g. a timed easy swim).
	DurationSecs int
	// Stroke is free, backstroke, breaststroke or fly; empty means any stroke.
	Stroke string
	// Drill marks the step as kick, pull or drill work; empty means a normal swim.
	Drill string
	// Equipment is fins, kickboard, paddles or pull_buoy; empty means none.
	Equipment string
	// Repeats performs the step that many times (>=1, default 1), expanded into
	// discrete steps with a rest between repeats.
	Repeats int
	// RestSecs inserts a timed rest step after each repeat except the last
	// (0 = no rest inserted between repeats).
	RestSecs int
	// Target is an optional pace or heart-rate intensity target.
	Target TargetSpec
	// Warmup/Cooldown mark the step type; both false means a working interval.
	Warmup   bool
	Cooldown bool
	// Notes is an optional coaching cue mapped to the Garmin step description.
	Notes string
}

// BuildSwimWorkout assembles a pool-swim Workout from a name, pool length in
// metres (0 defaults to 25) and a sequence of step specs. It is pure: no
// network, no time, no global state.
func BuildSwimWorkout(name string, poolLengthMeters float64, specs []SwimStepSpec) (*Workout, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("workout name is required")
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("workout %q has no steps", name)
	}
	if poolLengthMeters == 0 {
		poolLengthMeters = 25
	}
	if poolLengthMeters < 10 || poolLengthMeters > 150 {
		return nil, fmt.Errorf("pool length %g m is out of range (10-150)", poolLengthMeters)
	}

	// displayOrder 3 as harvested (it differs from the sport id).
	sport := sportType{SportTypeID: sportSwimID, SportTypeKey: sportSwimKey, DisplayOrder: 3}

	var steps []executableStep
	order := 1
	addStep := func(s executableStep) {
		s.StepOrder = order
		steps = append(steps, s)
		order++
	}

	for i, spec := range specs {
		if err := spec.Target.Validate(sportSwimKey); err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}

		// Standalone rest step (no distance, no duration).
		if spec.DistanceMeters == 0 && spec.DurationSecs == 0 {
			if spec.Stroke != "" || spec.Drill != "" || spec.Equipment != "" || spec.Target.Kind != TargetNone {
				return nil, fmt.Errorf("step %d: a swim step needs distance_m or duration_secs", i+1)
			}
			addStep(swimRestStep(spec.RestSecs))
			continue
		}
		if spec.DistanceMeters != 0 && spec.DurationSecs != 0 {
			return nil, fmt.Errorf("step %d: give distance_m or duration_secs, not both", i+1)
		}
		if spec.DistanceMeters < 0 || spec.DurationSecs < 0 || spec.RestSecs < 0 {
			return nil, fmt.Errorf("step %d: distance, duration and rest must be >= 0", i+1)
		}
		if spec.Warmup && spec.Cooldown {
			return nil, fmt.Errorf("step %d: a step cannot be both warmup and cooldown", i+1)
		}

		step, err := swimStep(spec)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}

		repeats := spec.Repeats
		if repeats < 1 {
			repeats = 1
		}
		for rep := 0; rep < repeats; rep++ {
			addStep(step)
			// Timed rest between repeats, never after the final one.
			if spec.RestSecs > 0 && rep < repeats-1 {
				addStep(swimRestStep(spec.RestSecs))
			}
		}
	}

	pool := poolLengthMeters
	return &Workout{
		WorkoutName:    name,
		SportType:      sport,
		PoolLength:     &pool,
		PoolLengthUnit: meter,
		WorkoutSegments: []segment{{
			SegmentOrder: 1,
			SportType:    sport,
			WorkoutSteps: steps,
		}},
	}, nil
}

// swimStep builds one executable swim step from a spec (repeats are expanded by
// the caller; StepOrder is stamped there too).
func swimStep(spec SwimStepSpec) (executableStep, error) {
	// Harvested mapping: working sets are "main" (8); drill steps are
	// "interval" (3); warmup/cooldown as named.
	st := stepType{StepTypeID: stepTypeMainID, StepTypeKey: stepTypeMainKey}
	switch {
	case spec.Warmup:
		st = stepType{StepTypeID: stepTypeWarmupID, StepTypeKey: stepTypeWarmupKey}
	case spec.Cooldown:
		st = stepType{StepTypeID: stepTypeCooldownID, StepTypeKey: stepTypeCooldownKey}
	case spec.Drill != "":
		st = stepType{StepTypeID: stepTypeInterval, StepTypeKey: stepTypeIntervKey}
	}

	step := executableStep{
		Type:     "ExecutableStepDTO",
		StepType: st,
	}
	if spec.DistanceMeters > 0 {
		step.EndCondition = endCondition{ConditionTypeID: condDistanceID, ConditionTypeKey: condDistanceKey}
		step.EndConditionValue = float64(spec.DistanceMeters)
		step.PreferredEndConditionUnit = meter
	} else {
		step.EndCondition = endCondition{ConditionTypeID: condTimeID, ConditionTypeKey: condTimeKey}
		step.EndConditionValue = float64(spec.DurationSecs)
	}

	// Harvested shape: strokeType present when a stroke is chosen; equipmentType
	// always present (all-null when none); drillType only when set.
	if key := strings.ToLower(strings.TrimSpace(spec.Stroke)); key != "" {
		if key == "any" {
			key = "any_stroke"
		}
		named, ok := strokeTypes[key]
		if !ok {
			return executableStep{}, fmt.Errorf("unknown stroke %q (want free, backstroke, breaststroke, fly or any_stroke)", spec.Stroke)
		}
		step.StrokeType = &named
	}

	equipment := equipmentType{}
	if key := strings.ToLower(strings.TrimSpace(spec.Equipment)); key != "" {
		named, ok := equipmentTypes[key]
		if !ok {
			return executableStep{}, fmt.Errorf("unknown equipment %q (want fins, kickboard, paddles, pull_buoy or snorkel)", spec.Equipment)
		}
		equipment = named
	}
	step.EquipmentType = &equipment

	if key := strings.ToLower(strings.TrimSpace(spec.Drill)); key != "" {
		named, ok := drillTypes[key]
		if !ok {
			return executableStep{}, fmt.Errorf("unknown drill %q (want kick, pull or drill)", spec.Drill)
		}
		step.DrillType = &named
	}

	spec.Target.applySwim(&step)

	if notes := strings.TrimSpace(spec.Notes); notes != "" {
		step.Description = &notes
	}
	return step, nil
}

// swimRestStep builds a rest step: a fixed.rest of restSecs seconds when
// restSecs > 0 (harvested), else lap-button.
func swimRestStep(restSecs int) executableStep {
	step := executableStep{
		Type:          "ExecutableStepDTO",
		StepType:      stepType{StepTypeID: stepTypeRestID, StepTypeKey: stepTypeRestKey},
		EndCondition:  endCondition{ConditionTypeID: condLapButtonID, ConditionTypeKey: condLapButtonKey},
		EquipmentType: &equipmentType{},
	}
	if restSecs > 0 {
		step.EndCondition = endCondition{ConditionTypeID: condFixedRestID, ConditionTypeKey: condFixedRest}
		step.EndConditionValue = float64(restSecs)
	}
	TargetSpec{}.apply(&step)
	return step
}
