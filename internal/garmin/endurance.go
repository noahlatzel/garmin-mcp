package garmin

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxEnduranceSteps = 100

type enduranceStepInput struct {
	Kind         string            `json:"kind,omitempty" jsonschema:"warmup, interval (default), recovery, rest or cooldown."`
	DurationSecs mcputil.FlexInt   `json:"duration_secs,omitempty" jsonschema:"End after this many seconds. Give duration_secs or distance_m, or omit both to advance with the lap button."`
	DistanceM    mcputil.FlexFloat `json:"distance_m,omitempty" jsonschema:"End after this many metres."`
	Repeats      mcputil.FlexInt   `json:"repeats,omitempty" jsonschema:"Repeat this step this many times, default 1."`
	RecoverySecs mcputil.FlexInt   `json:"recovery_secs,omitempty" jsonschema:"Optional recovery in seconds between repeats."`
	TargetKind   string            `json:"target_kind,omitempty" jsonschema:"running: pace or heart_rate. cycling: power, heart_rate or speed. Omit for no target."`
	TargetZone   mcputil.FlexInt   `json:"target_zone,omitempty" jsonschema:"Heart-rate zone 1–5 or power zone 1–7. Use a zone or a low/high range."`
	TargetLow    mcputil.FlexFloat `json:"target_low,omitempty" jsonschema:"Lower bound in metres per second for pace/speed, bpm for heart rate, or watts for power."`
	TargetHigh   mcputil.FlexFloat `json:"target_high,omitempty" jsonschema:"Upper bound in the same units. For pace, the faster pace is the higher speed."`
	Notes        string            `json:"notes,omitempty" jsonschema:"Optional coaching cue for the step."`
}

type enduranceSteps []enduranceStepInput

func (s *enduranceSteps) UnmarshalJSON(data []byte) error {
	steps, err := mcputil.UnmarshalStringifiedArray[enduranceStepInput](data)
	if err != nil {
		return err
	}
	*s = steps
	return nil
}

type enduranceWorkoutInput struct {
	Name         string           `json:"name" jsonschema:"Name to save in Garmin Connect."`
	Steps        enduranceSteps   `json:"steps" jsonschema:"Ordered workout steps. The expanded workout can have up to 100 steps."`
	ScheduleDate string           `json:"schedule_date,omitempty" jsonschema:"Optional Garmin calendar date, YYYY-MM-DD."`
	DryRun       mcputil.FlexBool `json:"dry_run,omitempty" jsonschema:"Preview the workout and planned date without writing to Garmin."`
	Verbose      mcputil.FlexBool `json:"verbose,omitempty" jsonschema:"Return the full Garmin creation response."`
}

func registerEnduranceTools(server *mcp.Server, svc *Service) {
	for _, tool := range []struct {
		name, description string
		sport             sportType
	}{
		{"create_run_workout", "Create a structured running workout in Garmin Connect with optional pace or heart-rate targets.", sportType{1, "running", 1}},
		{"create_cycling_workout", "Create a structured cycling workout in Garmin Connect with optional power, speed or heart-rate targets.", sportType{2, "cycling", 2}},
	} {
		schema := mcputil.InputSchema[enduranceWorkoutInput](tool.name, "dry_run", "verbose")
		widenArrayProp(schema, "steps", "duration_secs", "distance_m", "repeats", "recovery_secs", "target_zone", "target_low", "target_high")
		mcp.AddTool(server, &mcp.Tool{
			Name: tool.name,
			Description: tool.description + " Supports timed, distance and lap-button steps, repeats, " +
				"dry_run and optional calendar scheduling. In previews, time is in seconds, distance in metres, pace/speed targets in m/s, " +
				"heart-rate targets in bpm, power targets in watts, and zones are numbered." + nativeWorkoutUnits,
			InputSchema: schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false)},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in enduranceWorkoutInput) (*mcp.CallToolResult, any, error) {
			if in.ScheduleDate != "" {
				if _, err := time.Parse(time.DateOnly, in.ScheduleDate); err != nil {
					return nil, nil, fmt.Errorf("schedule_date must be a valid YYYY-MM-DD date")
				}
			}
			workout, err := buildEnduranceWorkout(tool.sport, in)
			if err != nil {
				return nil, nil, err
			}
			if in.DryRun {
				return mcputil.DataResult(map[string]any{"dry_run": true, "workout": workout, "schedule_date": in.ScheduleDate})
			}
			body, err := svc.CreateBuilt(ctx, workout, in.ScheduleDate)
			if err != nil {
				return nil, nil, err
			}
			return createResult(body, in.ScheduleDate, in.Verbose.Bool())
		})
	}
}

var enduranceStepTypes = map[string]stepType{
	"":         {stepTypeInterval, stepTypeIntervKey},
	"interval": {stepTypeInterval, stepTypeIntervKey},
	"warmup":   {stepTypeWarmupID, stepTypeWarmupKey},
	"cooldown": {stepTypeCooldownID, stepTypeCooldownKey},
	"recovery": {4, "recovery"},
	"rest":     {stepTypeRestID, stepTypeRestKey},
}

func buildEnduranceWorkout(sport sportType, in enduranceWorkoutInput) (*Workout, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(in.Steps) == 0 {
		return nil, fmt.Errorf("workout name and at least one step are required")
	}
	var steps []executableStep
	for i, input := range in.Steps {
		step, err := buildEnduranceStep(sport.SportTypeKey, input)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		repeats := max(1, input.Repeats.Int())
		for repeat := 0; repeat < repeats; repeat++ {
			step.StepOrder = len(steps) + 1
			steps = append(steps, step)
			if input.RecoverySecs > 0 && repeat+1 < repeats {
				recovery := executableStep{
					Type: "ExecutableStepDTO", StepOrder: len(steps) + 1,
					StepType: enduranceStepTypes["recovery"], EndCondition: endCondition{condTimeID, condTimeKey},
					EndConditionValue: float64(input.RecoverySecs),
				}
				TargetSpec{}.apply(&recovery)
				steps = append(steps, recovery)
			}
			if len(steps) > maxEnduranceSteps {
				return nil, fmt.Errorf("workout exceeds %d expanded steps", maxEnduranceSteps)
			}
		}
	}
	return &Workout{
		WorkoutName: name, SportType: sport,
		WorkoutSegments: []segment{{SegmentOrder: 1, SportType: sport, WorkoutSteps: steps}},
	}, nil
}

func buildEnduranceStep(sport string, in enduranceStepInput) (executableStep, error) {
	kind, ok := enduranceStepTypes[in.Kind]
	if !ok {
		return executableStep{}, fmt.Errorf("unsupported step kind %q", in.Kind)
	}
	distance := in.DistanceM.Float()
	if math.IsNaN(distance) || math.IsInf(distance, 0) || distance < 0 || in.DurationSecs < 0 || (distance > 0 && in.DurationSecs > 0) {
		return executableStep{}, fmt.Errorf("use either a positive duration_secs or distance_m, or omit both")
	}
	if in.Repeats < 0 || in.Repeats > maxEnduranceSteps || in.RecoverySecs < 0 || (in.RecoverySecs > 0 && in.Repeats < 2) {
		return executableStep{}, fmt.Errorf("repeats must be 1–%d when specified, and recovery_secs requires repeats of at least 2", maxEnduranceSteps)
	}
	target := TargetSpec{Kind: in.TargetKind, Zone: in.TargetZone.Int(), Low: in.TargetLow.Float(), High: in.TargetHigh.Float()}
	if math.IsNaN(target.Low) || math.IsInf(target.Low, 0) || math.IsNaN(target.High) || math.IsInf(target.High, 0) {
		return executableStep{}, fmt.Errorf("target bounds must be finite")
	}
	if target.Zone != 0 && (target.Kind == TargetPace || target.Kind == TargetSpeed || (target.Kind == TargetHeartRate && target.Zone > 5)) {
		return executableStep{}, fmt.Errorf("use a range for pace/speed or a zone of 1–5 for heart rate")
	}
	if err := target.Validate(sport); err != nil {
		return executableStep{}, err
	}
	step := executableStep{Type: "ExecutableStepDTO", StepType: kind, EndCondition: endCondition{condLapButtonID, condLapButtonKey}}
	switch {
	case in.DurationSecs > 0:
		step.EndCondition = endCondition{condTimeID, condTimeKey}
		step.EndConditionValue = float64(in.DurationSecs)
	case distance > 0:
		step.EndCondition = endCondition{condDistanceID, condDistanceKey}
		step.EndConditionValue = distance
		step.PreferredEndConditionUnit = meter
	}
	if in.Notes != "" {
		step.Description = &in.Notes
	}
	target.apply(&step)
	return step, nil
}
