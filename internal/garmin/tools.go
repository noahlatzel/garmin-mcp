package garmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
)

const (
	serverName    = "garmin"
	serverVersion = "0.1.0"
)

// dateRe matches an ISO calendar date (YYYY-MM-DD), validated client-side before
// any connectapi call.
var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// validateDate returns an error if date is not YYYY-MM-DD. An empty date is
// allowed only when optional is true (used for optional schedule dates).
func validateDate(field, date string, optional bool) error {
	if date == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("%s is required (YYYY-MM-DD)", field)
	}
	if !dateRe.MatchString(date) {
		return fmt.Errorf("%s must be YYYY-MM-DD, got %q", field, date)
	}
	return nil
}

// StepInput is one exercise in a create_strength_workout request. Scalar fields
// use the mcputil Flex types so a client that stringifies booleans/numbers
// ("true", "5") still decodes; the published schema is widened to match.
type StepInput struct {
	Exercise        string            `json:"exercise" jsonschema:"Exercise display name or Garmin key (e.g. 'Barbell Back Squat' or BARBELL_BACK_SQUAT). Leave empty for a standalone rest step."`
	Sets            mcputil.FlexInt   `json:"sets,omitempty" jsonschema:"Number of sets (>=1). Expanded into discrete steps; defaults to 1."`
	Reps            mcputil.FlexInt   `json:"reps,omitempty" jsonschema:"Reps per set. When set, the step ends on reps; otherwise it ends on the lap button."`
	WeightKg        mcputil.FlexFloat `json:"weight_kg,omitempty" jsonschema:"Optional working weight in kilograms."`
	DurationSecs    mcputil.FlexInt   `json:"duration_secs,omitempty" jsonschema:"Optional time-based step length in seconds (e.g. a warmup row). Reps takes precedence if both are set."`
	Warmup          mcputil.FlexBool  `json:"warmup,omitempty" jsonschema:"Mark this step as a warmup rather than a working interval."`
	RestBetweenSets *mcputil.FlexBool `json:"rest_between_sets,omitempty" jsonschema:"Insert a lap-button rest step between sets (default true). Set false to expand the sets back-to-back with no rest."`
	Notes           string            `json:"notes,omitempty" jsonschema:"Optional coaching cue (weight guidance, tempo, technique) shown as the Garmin step notes. Kept separate from reps and weight; applies to every set of the exercise."`
}

// StepInputs is an ordered list of workout steps that tolerates a client sending
// the array either natively or as a JSON-encoded string. Some MCP clients
// (observed with the Claude/Anthropic transport) stringify structured
// array/object arguments; without this the SDK's pre-handler schema validation
// rejects the call with `has type "string", want ... "array"`.
type StepInputs []StepInput

// UnmarshalJSON accepts a native JSON array of steps or a JSON string whose
// contents are a JSON array of steps (double-encoded), decoding both to the same
// slice. A JSON null decodes to a nil slice.
func (s *StepInputs) UnmarshalJSON(data []byte) error {
	arr, err := mcputil.UnmarshalStringifiedArray[StepInput](data)
	if err != nil {
		return fmt.Errorf("steps: %w", err)
	}
	*s = arr
	return nil
}

// CreateStrengthWorkoutInput is the input for create_strength_workout.
type CreateStrengthWorkoutInput struct {
	Name         string           `json:"name" jsonschema:"The workout name as it will appear in Garmin Connect."`
	Steps        StepInputs       `json:"steps" jsonschema:"Ordered list of exercises. One exercise per step; sets are expanded into discrete steps."`
	ScheduleDate string           `json:"schedule_date,omitempty" jsonschema:"Optional date (YYYY-MM-DD) to also schedule the created workout on the Garmin calendar in one call."`
	DryRun       mcputil.FlexBool `json:"dry_run,omitempty" jsonschema:"If true, return the workout (and planned schedule) that would be sent without sending it."`
	Verbose      mcputil.FlexBool `json:"verbose,omitempty" jsonschema:"If true, return the full Garmin created-workout JSON instead of the compact {workout_id, name, sport, schedule_date, scheduled} summary."`
}

// strengthSpecs converts tool-level strength step inputs into builder specs.
func strengthSpecs(steps StepInputs) []StepSpec {
	specs := make([]StepSpec, len(steps))
	for i, s := range steps {
		spec := StepSpec{
			Exercise:     s.Exercise,
			Sets:         s.Sets.Int(),
			Reps:         s.Reps.Int(),
			DurationSecs: s.DurationSecs.Int(),
			Warmup:       s.Warmup.Bool(),
			Notes:        s.Notes,
		}
		if s.RestBetweenSets != nil {
			rest := s.RestBetweenSets.Bool()
			spec.RestBetweenSets = &rest
		}
		if s.WeightKg > 0 {
			w := s.WeightKg.Float()
			spec.WeightKg = &w
		}
		specs[i] = spec
	}
	return specs
}

// widenArrayProp widens a named array property to also accept a JSON-encoded
// string of that array (the container types' UnmarshalJSON reconstructs
// either), and widens the named scalar properties of the array's item schema.
// It returns the item schema (nil if absent) so nested arrays can be widened
// in turn.
func widenArrayProp(schema *jsonschema.Schema, prop string, itemProps ...string) *jsonschema.Schema {
	arr, ok := schema.Properties[prop]
	if !ok {
		return nil
	}
	arr.Type = ""
	arr.Types = []string{"array", "string", "null"}
	if arr.Items != nil {
		mcputil.WidenStringified(arr.Items, itemProps...)
	}
	return arr.Items
}

// widenSteps widens the "steps" property of a single-workout create schema.
func widenSteps(schema *jsonschema.Schema, itemProps ...string) {
	widenArrayProp(schema, "steps", itemProps...)
}

// strengthStepProps and swimStepProps are the per-step scalar properties that
// must also accept their stringified form (see mcputil.WidenStringified).
var (
	strengthStepProps = []string{"sets", "reps", "weight_kg", "duration_secs", "warmup", "rest_between_sets"}
	swimStepProps     = []string{"distance_m", "duration_secs", "repeats", "rest_secs", "target_hr_zone", "warmup", "cooldown"}
)

// strengthWorkoutInputSchema returns the create_strength_workout input schema
// widened for stringifying clients: steps as array|string, and every scalar
// (top-level and per-step) also accepting its stringified form.
func strengthWorkoutInputSchema() *jsonschema.Schema {
	schema := mcputil.InputSchema[CreateStrengthWorkoutInput]("create_strength_workout", "dry_run", "verbose")
	widenSteps(schema, strengthStepProps...)
	return schema
}

// SwimStepInput is one step in a create_swim_workout request.
type SwimStepInput struct {
	DistanceMeters mcputil.FlexInt  `json:"distance_m,omitempty" jsonschema:"Step distance in metres (multiple of the pool length). Give distance_m or duration_secs, not both; a step with neither is a standalone rest."`
	DurationSecs   mcputil.FlexInt  `json:"duration_secs,omitempty" jsonschema:"Time-based step length in seconds (alternative to distance_m)."`
	Stroke         string           `json:"stroke,omitempty" jsonschema:"Stroke: free, backstroke, breaststroke, fly or any_stroke. Empty omits the stroke."`
	Drill          string           `json:"drill,omitempty" jsonschema:"Drill type: kick, pull or drill. Empty = normal swim."`
	Equipment      string           `json:"equipment,omitempty" jsonschema:"Equipment: fins, kickboard, paddles, pull_buoy or snorkel. Empty = none."`
	Repeats        mcputil.FlexInt  `json:"repeats,omitempty" jsonschema:"Perform the step this many times (default 1). Expanded into discrete steps with a rest between repeats."`
	RestSecs       mcputil.FlexInt  `json:"rest_secs,omitempty" jsonschema:"Timed rest in seconds inserted after each repeat except the last."`
	PacePer100m    string           `json:"target_pace_per_100m,omitempty" jsonschema:"Target pace per 100 m as a single value, e.g. '1:45' (Garmin swim pace targets are single-valued, not ranges)."`
	HRZone         mcputil.FlexInt  `json:"target_hr_zone,omitempty" jsonschema:"Target heart-rate zone (1-5). Alternative to a pace target."`
	Warmup         mcputil.FlexBool `json:"warmup,omitempty" jsonschema:"Mark this step as a warmup."`
	Cooldown       mcputil.FlexBool `json:"cooldown,omitempty" jsonschema:"Mark this step as a cooldown."`
	Notes          string           `json:"notes,omitempty" jsonschema:"Optional coaching cue shown as the Garmin step notes."`
}

// SwimStepInputs tolerates a client sending the array natively or as a
// JSON-encoded string, like StepInputs.
type SwimStepInputs []SwimStepInput

// UnmarshalJSON accepts a native JSON array of steps or a JSON string whose
// contents are a JSON array of steps. A JSON null decodes to a nil slice.
func (s *SwimStepInputs) UnmarshalJSON(data []byte) error {
	arr, err := mcputil.UnmarshalStringifiedArray[SwimStepInput](data)
	if err != nil {
		return fmt.Errorf("steps: %w", err)
	}
	*s = arr
	return nil
}

// CreateSwimWorkoutInput is the input for create_swim_workout.
type CreateSwimWorkoutInput struct {
	Name         string            `json:"name" jsonschema:"The workout name as it will appear in Garmin Connect."`
	PoolLengthM  mcputil.FlexFloat `json:"pool_length_m,omitempty" jsonschema:"Pool length in metres (default 25)."`
	Steps        SwimStepInputs    `json:"steps" jsonschema:"Ordered list of swim steps. Repeats are expanded into discrete steps."`
	ScheduleDate string            `json:"schedule_date,omitempty" jsonschema:"Optional date (YYYY-MM-DD) to also schedule the created workout on the Garmin calendar in one call."`
	DryRun       mcputil.FlexBool  `json:"dry_run,omitempty" jsonschema:"If true, return the workout (and planned schedule) that would be sent without sending it."`
	Verbose      mcputil.FlexBool  `json:"verbose,omitempty" jsonschema:"If true, return the full Garmin created-workout JSON instead of the compact {workout_id, name, sport, schedule_date, scheduled} summary."`
}

// swimSpecs converts tool-level swim step inputs into builder specs, resolving
// the flat target fields. Errors carry the 1-indexed step number.
func swimSpecs(steps SwimStepInputs) ([]SwimStepSpec, error) {
	specs := make([]SwimStepSpec, len(steps))
	for i, s := range steps {
		target, err := swimTargetSpec(s)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		specs[i] = SwimStepSpec{
			DistanceMeters: s.DistanceMeters.Int(),
			DurationSecs:   s.DurationSecs.Int(),
			Stroke:         s.Stroke,
			Drill:          s.Drill,
			Equipment:      s.Equipment,
			Repeats:        s.Repeats.Int(),
			RestSecs:       s.RestSecs.Int(),
			Target:         target,
			Warmup:         s.Warmup.Bool(),
			Cooldown:       s.Cooldown.Bool(),
			Notes:          s.Notes,
		}
	}
	return specs, nil
}

// swimWorkoutInputSchema mirrors strengthWorkoutInputSchema for swim.
func swimWorkoutInputSchema() *jsonschema.Schema {
	schema := mcputil.InputSchema[CreateSwimWorkoutInput]("create_swim_workout", "dry_run", "pool_length_m", "verbose")
	widenSteps(schema, swimStepProps...)
	return schema
}

// maxBatchWorkouts caps a create_workouts batch (two weeks of daily workouts).
const maxBatchWorkouts = 14

// WorkoutSpecInput is one item in a create_workouts batch: a strength or swim
// workout with the same step rules as the single-workout tools.
type WorkoutSpecInput struct {
	Type         string            `json:"type" jsonschema:"Workout type: 'strength' or 'swim'."`
	Name         string            `json:"name" jsonschema:"The workout name as it will appear in Garmin Connect."`
	Steps        StepInputs        `json:"steps,omitempty" jsonschema:"Strength steps (required when type=strength; same rules as create_strength_workout)."`
	SwimSteps    SwimStepInputs    `json:"swim_steps,omitempty" jsonschema:"Swim steps (required when type=swim; same rules as create_swim_workout)."`
	PoolLengthM  mcputil.FlexFloat `json:"pool_length_m,omitempty" jsonschema:"Pool length in metres for swim items (default 25)."`
	ScheduleDate string            `json:"schedule_date,omitempty" jsonschema:"Optional date (YYYY-MM-DD) to schedule this workout on the Garmin calendar."`
}

// WorkoutSpecInputs tolerates a client sending the batch natively or as a
// JSON-encoded string, like StepInputs.
type WorkoutSpecInputs []WorkoutSpecInput

// UnmarshalJSON accepts a native JSON array of workout specs or a JSON string
// whose contents are such an array. A JSON null decodes to a nil slice.
func (s *WorkoutSpecInputs) UnmarshalJSON(data []byte) error {
	arr, err := mcputil.UnmarshalStringifiedArray[WorkoutSpecInput](data)
	if err != nil {
		return fmt.Errorf("workouts: %w", err)
	}
	*s = arr
	return nil
}

// CreateWorkoutsInput is the input for create_workouts.
type CreateWorkoutsInput struct {
	Workouts WorkoutSpecInputs `json:"workouts" jsonschema:"Ordered list of workouts to create (max 14), each strength or swim."`
	DryRun   mcputil.FlexBool  `json:"dry_run,omitempty" jsonschema:"If true, validate and build every workout and return a compact per-item preview without creating anything."`
}

// createWorkoutsInputSchema widens the batch schema for stringifying clients:
// the workouts array (and each item's nested steps arrays and scalars) also
// accept their stringified forms.
func createWorkoutsInputSchema() *jsonschema.Schema {
	schema := mcputil.InputSchema[CreateWorkoutsInput]("create_workouts", "dry_run")
	item := widenArrayProp(schema, "workouts", "pool_length_m")
	if item != nil {
		widenArrayProp(item, "steps", strengthStepProps...)
		widenArrayProp(item, "swim_steps", swimStepProps...)
	}
	return schema
}

// swimTargetSpec converts the flat tool-level target fields into a TargetSpec.
func swimTargetSpec(in SwimStepInput) (TargetSpec, error) {
	pace := strings.TrimSpace(in.PacePer100m)
	if pace != "" && in.HRZone != 0 {
		return TargetSpec{}, fmt.Errorf("give target_pace_per_100m or target_hr_zone, not both")
	}
	if in.HRZone != 0 {
		return TargetSpec{Kind: TargetHeartRate, Zone: in.HRZone.Int()}, nil
	}
	if pace == "" {
		return TargetSpec{}, nil
	}
	if strings.Contains(pace, "-") {
		return TargetSpec{}, fmt.Errorf("target_pace_per_100m must be a single pace like '1:45' (Garmin swim pace targets are not ranges)")
	}
	mps, err := ParsePacePer100m(pace)
	if err != nil {
		return TargetSpec{}, err
	}
	return TargetSpec{Kind: TargetPace, Low: mps, High: mps}, nil
}

// SearchExercisesInput is the input for search_exercises.
type SearchExercisesInput struct {
	Query string          `json:"query" jsonschema:"Substring of the exercise display name to search for (case-insensitive), e.g. 'squat'."`
	Limit mcputil.FlexInt `json:"limit,omitempty" jsonschema:"Max matches to return (default 20)."`
}

// UnscheduleWorkoutInput identifies the single calendar entry
// unschedule_workout removes, gated by confirm/dry_run.
type UnscheduleWorkoutInput struct {
	ScheduleID string           `json:"schedule_id" jsonschema:"The Garmin workoutScheduleId of the calendar entry (returned by schedule_workout / carried on get_calendar items)."`
	Confirm    mcputil.FlexBool `json:"confirm,omitempty" jsonschema:"Must be true to actually remove the calendar entry."`
	DryRun     mcputil.FlexBool `json:"dry_run,omitempty" jsonschema:"If true, return the request that would be sent without sending it."`
}

// RenameWorkoutInput is the input for update_workout_name.
type RenameWorkoutInput struct {
	WorkoutID string           `json:"workout_id" jsonschema:"The Garmin workoutId to rename."`
	Name      string           `json:"name" jsonschema:"The new workout name."`
	Confirm   mcputil.FlexBool `json:"confirm,omitempty" jsonschema:"Must be true to apply the rename."`
	DryRun    mcputil.FlexBool `json:"dry_run,omitempty" jsonschema:"If true, return the rename that would be applied without applying it."`
}

// ScheduleWorkoutInput is the input for schedule_workout.
type ScheduleWorkoutInput struct {
	WorkoutID string           `json:"workout_id" jsonschema:"The Garmin workoutId to schedule."`
	Date      string           `json:"date" jsonschema:"Calendar date (YYYY-MM-DD) to schedule the workout on."`
	DryRun    mcputil.FlexBool `json:"dry_run,omitempty" jsonschema:"If true, return the request that would be sent without sending it."`
}

// ListWorkoutsInput is the input for list_workouts.
type ListWorkoutsInput struct {
	Limit mcputil.FlexInt `json:"limit,omitempty" jsonschema:"Max workouts to return (default 20)."`
}

// GetWorkoutInput identifies the workout get_workout fetches.
type GetWorkoutInput struct {
	WorkoutID string `json:"workout_id" jsonschema:"The Garmin workoutId."`
}

// DeleteWorkoutInput identifies the single workout delete_workout removes,
// gated by confirm/dry_run.
type DeleteWorkoutInput struct {
	WorkoutID string           `json:"workout_id" jsonschema:"The Garmin workoutId."`
	Confirm   mcputil.FlexBool `json:"confirm,omitempty" jsonschema:"Must be true to actually delete."`
	DryRun    mcputil.FlexBool `json:"dry_run,omitempty" jsonschema:"If true, return the workout that would be deleted without deleting."`
}

// CalendarInput is the input for get_calendar.
type CalendarInput struct {
	Year  mcputil.FlexInt `json:"year" jsonschema:"Four-digit year."`
	Month mcputil.FlexInt `json:"month" jsonschema:"Month, 1-indexed (1=January, 12=December)."`
}

// DateInput identifies a single date (get_daily_summary/get_sleep/get_hrv/get_stress).
type DateInput struct {
	Date string `json:"date" jsonschema:"Calendar date (YYYY-MM-DD)."`
}

// DateRangeInput is an inclusive date range (get_body_battery).
type DateRangeInput struct {
	StartDate string `json:"start_date" jsonschema:"Inclusive start date (YYYY-MM-DD)."`
	EndDate   string `json:"end_date" jsonschema:"Inclusive end date (YYYY-MM-DD)."`
}

// NewMCPServer builds the garmin MCP server, backed by the given Service: the
// workout writers (strength, pool swim), workout/calendar management, and the
// wellness reads.
func NewMCPServer(svc *Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "create_strength_workout",
		Description: "Create a native strength workout in Garmin Connect. Each step is one " +
			"named exercise with reps and optional weight (kg); sets are expanded into discrete " +
			"steps (never merged, never an Nx block). Use search_exercises first to resolve exact " +
			"exercise names. Optionally pass schedule_date (YYYY-MM-DD) to also put it on the " +
			"calendar in one call. Returns a compact {workout_id, name, sport, schedule_date, " +
			"scheduled} summary by default; pass verbose=true for the full Garmin JSON. For " +
			"several workouts (e.g. a training week) prefer create_workouts. Supports dry_run. " +
			"Requires the one-time Garmin account link.",
		InputSchema: strengthWorkoutInputSchema(),
	}, handleCreateStrengthWorkout(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name: "create_swim_workout",
		Description: "Create a native pool-swim workout in Garmin Connect. Each step is a swim " +
			"(distance in metres or a time), with optional stroke (free/backstroke/breaststroke/fly), " +
			"drill (kick/pull/drill), equipment (fins/kickboard/paddles/pull_buoy), a pace-per-100m or " +
			"HR-zone target, and repeats (expanded into discrete steps with timed rests — never an Nx " +
			"block). pool_length_m defaults to 25. Optionally pass schedule_date (YYYY-MM-DD) to " +
			"also put it on the calendar in one call. Returns a compact {workout_id, name, sport, " +
			"schedule_date, scheduled} summary by default; pass verbose=true for the full Garmin " +
			"JSON. For several workouts (e.g. a training week) prefer create_workouts. Supports " +
			"dry_run. Requires the one-time Garmin account link.",
		InputSchema: swimWorkoutInputSchema(),
	}, handleCreateSwimWorkout(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name: "create_workouts",
		Description: "Create several Garmin workouts (strength and/or swim, max 14) in ONE call — " +
			"prefer this over repeated create_strength_workout/create_swim_workout calls when " +
			"building a training week. Each item follows the same step rules as the single-workout " +
			"tools and may carry its own schedule_date. Every workout is validated and built " +
			"locally first: any invalid item (e.g. unknown exercise) aborts the whole batch before " +
			"anything is created. Creation then continues past per-item network failures, " +
			"returning a compact per-item {index, name, status, workout_id} report so only failed " +
			"items need retrying (status created_unscheduled means the workout exists — use " +
			"schedule_workout, don't re-create). Supports dry_run. Requires the one-time Garmin " +
			"account link.",
		InputSchema: createWorkoutsInputSchema(),
	}, handleCreateWorkouts(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name: "search_exercises",
		Description: "Search the embedded Garmin strength-exercise catalog by name substring " +
			"(case-insensitive). Returns matching {name, category, exercise} entries. Use this to " +
			"resolve exact exercise names BEFORE calling create_strength_workout or " +
			"create_workouts, instead of guessing.",
		InputSchema: mcputil.InputSchema[SearchExercisesInput]("search_exercises", "limit"),
	}, handleSearchExercises(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name: "schedule_workout",
		Description: "Schedule an existing Garmin workout on the calendar by workoutId + date " +
			"(YYYY-MM-DD). Reversible via unschedule_workout. Supports dry_run.",
		InputSchema: mcputil.InputSchema[ScheduleWorkoutInput]("schedule_workout", "dry_run"),
	}, handleScheduleWorkout(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name: "unschedule_workout",
		Description: "Remove one scheduled entry from the Garmin calendar by its " +
			"workoutScheduleId (from schedule_workout's response or get_calendar items). The " +
			"saved workout itself is untouched. Only removes the single given id with " +
			"confirm=true (default false). Supports dry_run. Never bulk-removes.",
		InputSchema: mcputil.InputSchema[UnscheduleWorkoutInput]("unschedule_workout", "confirm", "dry_run"),
	}, handleUnscheduleWorkout(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name: "update_workout_name",
		Description: "Rename one saved Garmin workout by workoutId (read-modify-write of the " +
			"full workout body; steps are untouched, and the rename is verified by re-fetching). " +
			"Applies only with confirm=true (default false). Supports dry_run.",
		InputSchema: mcputil.InputSchema[RenameWorkoutInput]("update_workout_name", "confirm", "dry_run"),
	}, handleRenameWorkout(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_workouts",
		Description: "List the user's saved Garmin workouts (id, name, type), newest first.",
		InputSchema: mcputil.InputSchema[ListWorkoutsInput]("list_workouts", "limit"),
	}, handleListWorkouts(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_workout",
		Description: "Fetch one Garmin workout's full JSON by workoutId.",
		InputSchema: mcputil.InputSchema[GetWorkoutInput]("get_workout"),
	}, handleGetWorkout(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name: "delete_workout",
		Description: "Delete one Garmin workout by workoutId. First fetches and returns the " +
			"workout; only deletes the single given id with confirm=true (default false). " +
			"Supports dry_run. Never bulk-deletes.",
		InputSchema: mcputil.InputSchema[DeleteWorkoutInput]("delete_workout", "confirm", "dry_run"),
	}, handleDeleteWorkout(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_calendar",
		Description: "Read scheduled Garmin calendar items for a given year and month (1-indexed).",
		InputSchema: mcputil.InputSchema[CalendarInput]("get_calendar", "year", "month"),
	}, handleGetCalendar(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_daily_summary",
		Description: "Read a day's Garmin daily summary: steps, calories, intensity minutes, " +
			"resting HR, body battery range. date is YYYY-MM-DD.",
	}, handleDateRead(svc.GetDailySummary))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_sleep",
		Description: "Read Garmin sleep stages, duration and sleep score for a date (YYYY-MM-DD).",
	}, handleDateRead(svc.GetSleep))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_hrv",
		Description: "Read overnight HRV (status + readings) for a date (YYYY-MM-DD).",
	}, handleDateRead(svc.GetHRV))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_body_battery",
		Description: "Read the Garmin body-battery timeline over an inclusive date range (YYYY-MM-DD).",
	}, handleGetBodyBattery(svc))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_stress",
		Description: "Read the Garmin daily stress timeline for a date (YYYY-MM-DD).",
	}, handleDateRead(svc.GetStress))

	return server
}

func handleCreateStrengthWorkout(svc *Service) func(context.Context, *mcp.CallToolRequest, CreateStrengthWorkoutInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args CreateStrengthWorkoutInput) (*mcp.CallToolResult, any, error) {
		if err := validateDate("schedule_date", args.ScheduleDate, true); err != nil {
			return nil, nil, err
		}

		specs := strengthSpecs(args.Steps)

		if args.DryRun {
			workout, err := svc.Catalog.BuildWorkout(args.Name, specs)
			if err != nil {
				return nil, nil, err
			}
			return mcputil.DataResult(map[string]any{
				"dry_run":       true,
				"workout":       workout,
				"schedule_date": args.ScheduleDate,
			})
		}

		body, err := svc.CreateStrengthWorkout(ctx, args.Name, specs, args.ScheduleDate)
		if err != nil {
			return nil, nil, err
		}
		return createResult(body, args.ScheduleDate, args.Verbose.Bool())
	}
}

func handleCreateSwimWorkout(svc *Service) func(context.Context, *mcp.CallToolRequest, CreateSwimWorkoutInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args CreateSwimWorkoutInput) (*mcp.CallToolResult, any, error) {
		if err := validateDate("schedule_date", args.ScheduleDate, true); err != nil {
			return nil, nil, err
		}

		specs, err := swimSpecs(args.Steps)
		if err != nil {
			return nil, nil, err
		}

		if args.DryRun {
			workout, err := BuildSwimWorkout(args.Name, args.PoolLengthM.Float(), specs)
			if err != nil {
				return nil, nil, err
			}
			return mcputil.DataResult(map[string]any{
				"dry_run":       true,
				"workout":       workout,
				"schedule_date": args.ScheduleDate,
			})
		}

		body, err := svc.CreateSwimWorkout(ctx, args.Name, args.PoolLengthM.Float(), specs, args.ScheduleDate)
		if err != nil {
			return nil, nil, err
		}
		return createResult(body, args.ScheduleDate, args.Verbose.Bool())
	}
}

// createResult renders a create-workout response: the compact
// {workout_id, name, sport, schedule_date, scheduled} summary by default, or
// the full Garmin created-workout JSON when verbose is true.
func createResult(created []byte, scheduleDate string, verbose bool) (*mcp.CallToolResult, any, error) {
	if verbose {
		res := mcputil.JSONResult(created)
		return res, nil, nil
	}
	compact, err := compactCreateResult(created, scheduleDate)
	if err != nil {
		return nil, nil, err
	}
	return mcputil.DataResult(compact)
}

// builtWorkout is one validated, fully-built batch item awaiting creation.
type builtWorkout struct {
	workoutType  string
	name         string
	scheduleDate string
	inputSteps   int
	workout      *Workout
}

// buildBatchItem validates and builds one create_workouts item without any
// network call, so the whole batch can be checked before the first write.
func buildBatchItem(catalog *Catalog, in WorkoutSpecInput) (builtWorkout, error) {
	if err := validateDate("schedule_date", in.ScheduleDate, true); err != nil {
		return builtWorkout{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(in.Type))
	b := builtWorkout{workoutType: kind, name: in.Name, scheduleDate: in.ScheduleDate}
	switch kind {
	case "strength":
		if len(in.Steps) == 0 {
			return builtWorkout{}, fmt.Errorf("steps is required for a strength workout")
		}
		workout, err := catalog.BuildWorkout(in.Name, strengthSpecs(in.Steps))
		if err != nil {
			return builtWorkout{}, err
		}
		b.inputSteps = len(in.Steps)
		b.workout = workout
	case "swim":
		if len(in.SwimSteps) == 0 {
			return builtWorkout{}, fmt.Errorf("swim_steps is required for a swim workout")
		}
		specs, err := swimSpecs(in.SwimSteps)
		if err != nil {
			return builtWorkout{}, err
		}
		workout, err := BuildSwimWorkout(in.Name, in.PoolLengthM.Float(), specs)
		if err != nil {
			return builtWorkout{}, err
		}
		b.inputSteps = len(in.SwimSteps)
		b.workout = workout
	default:
		return builtWorkout{}, fmt.Errorf("type must be 'strength' or 'swim', got %q", in.Type)
	}
	return b, nil
}

func handleCreateWorkouts(svc *Service) func(context.Context, *mcp.CallToolRequest, CreateWorkoutsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args CreateWorkoutsInput) (*mcp.CallToolResult, any, error) {
		if len(args.Workouts) == 0 {
			return nil, nil, fmt.Errorf("workouts is required (1-%d items)", maxBatchWorkouts)
		}
		if len(args.Workouts) > maxBatchWorkouts {
			return nil, nil, fmt.Errorf("too many workouts: %d (max %d per call)", len(args.Workouts), maxBatchWorkouts)
		}

		// Phase 1: validate and build every item locally. Any failure aborts
		// the whole batch before the first network call.
		built := make([]builtWorkout, len(args.Workouts))
		for i, w := range args.Workouts {
			b, err := buildBatchItem(svc.Catalog, w)
			if err != nil {
				return nil, nil, fmt.Errorf("workout %d (%q): %w — nothing was created", i+1, w.Name, err)
			}
			built[i] = b
		}

		if args.DryRun {
			previews := make([]map[string]any, len(built))
			for i, b := range built {
				p := map[string]any{
					"index": i + 1, "type": b.workoutType, "name": b.name, "steps": b.inputSteps,
				}
				if b.scheduleDate != "" {
					p["schedule_date"] = b.scheduleDate
				}
				previews[i] = p
			}
			return mcputil.DataResult(map[string]any{"dry_run": true, "workouts": previews})
		}

		// Phase 2: create sequentially, continuing past failures so the caller
		// gets a per-item picture and can retry only the failed indices.
		results := make([]map[string]any, len(built))
		var created, failed int
		for i, b := range built {
			res := map[string]any{"index": i + 1, "name": b.name}
			if b.scheduleDate != "" {
				res["schedule_date"] = b.scheduleDate
			}
			body, err := svc.CreateBuilt(ctx, b.workout, b.scheduleDate)
			switch {
			case err == nil:
				res["status"] = "created"
				created++
				if compact, cerr := compactCreateResult(body, b.scheduleDate); cerr == nil {
					res["workout_id"] = compact["workout_id"]
				}
			default:
				var sf *ScheduleFailedError
				if errors.As(err, &sf) {
					// Created but not scheduled: surface the id so the caller can
					// schedule_workout instead of re-creating.
					res["status"] = "created_unscheduled"
					res["workout_id"] = sf.WorkoutID
					res["error"] = sf.Err.Error()
					created++
				} else {
					res["status"] = "failed"
					res["error"] = err.Error()
					failed++
				}
			}
			results[i] = res
		}
		return mcputil.DataResult(map[string]any{
			"requested": len(built), "created": created, "failed": failed, "results": results,
		})
	}
}

func handleSearchExercises(svc *Service) func(context.Context, *mcp.CallToolRequest, SearchExercisesInput) (*mcp.CallToolResult, any, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, args SearchExercisesInput) (*mcp.CallToolResult, any, error) {
		hits, err := svc.Catalog.Search(args.Query, args.Limit.Int())
		if err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(map[string]any{"count": len(hits), "exercises": hits})
	}
}

func handleUnscheduleWorkout(svc *Service) func(context.Context, *mcp.CallToolRequest, UnscheduleWorkoutInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args UnscheduleWorkoutInput) (*mcp.CallToolResult, any, error) {
		if args.ScheduleID == "" {
			return nil, nil, fmt.Errorf("schedule_id is required")
		}
		if args.DryRun || !args.Confirm {
			reason := "confirm is false; pass confirm=true to remove the calendar entry"
			if args.DryRun {
				reason = "dry_run"
			}
			return mcputil.DataResult(map[string]any{
				"unscheduled": false, "reason": reason, "schedule_id": args.ScheduleID,
			})
		}
		if _, err := svc.UnscheduleWorkout(ctx, args.ScheduleID); err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(map[string]any{
			"unscheduled": true, "schedule_id": args.ScheduleID,
		})
	}
}

func handleRenameWorkout(svc *Service) func(context.Context, *mcp.CallToolRequest, RenameWorkoutInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args RenameWorkoutInput) (*mcp.CallToolResult, any, error) {
		if args.WorkoutID == "" {
			return nil, nil, fmt.Errorf("workout_id is required")
		}
		if strings.TrimSpace(args.Name) == "" {
			return nil, nil, fmt.Errorf("name is required")
		}
		if args.DryRun || !args.Confirm {
			reason := "confirm is false; pass confirm=true to rename"
			if args.DryRun {
				reason = "dry_run"
			}
			return mcputil.DataResult(map[string]any{
				"renamed": false, "reason": reason,
				"workout_id": args.WorkoutID, "new_name": args.Name,
			})
		}
		oldName, err := svc.RenameWorkout(ctx, args.WorkoutID, args.Name)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(map[string]any{
			"renamed": true, "workout_id": args.WorkoutID,
			"old_name": oldName, "new_name": args.Name,
		})
	}
}

// compactCreateResult extracts the identifying fields from a Garmin
// created-workout response (optionally carrying the merged "schedule" key from
// a create+schedule call), dropping the expanded step JSON.
func compactCreateResult(created []byte, scheduleDate string) (map[string]any, error) {
	var env struct {
		WorkoutID   json.Number `json:"workoutId"`
		WorkoutName string      `json:"workoutName"`
		SportType   struct {
			SportTypeKey string `json:"sportTypeKey"`
		} `json:"sportType"`
		Schedule json.RawMessage `json:"schedule"`
	}
	if err := json.Unmarshal(created, &env); err != nil {
		return nil, fmt.Errorf("parsing created workout: %w", err)
	}
	if env.WorkoutID == "" {
		return nil, fmt.Errorf("created workout response had no workoutId")
	}
	out := map[string]any{
		"workout_id": env.WorkoutID.String(),
		"name":       env.WorkoutName,
		"sport":      env.SportType.SportTypeKey,
		"scheduled":  len(env.Schedule) > 0,
	}
	if scheduleDate != "" {
		out["schedule_date"] = scheduleDate
	}
	return out, nil
}

func handleScheduleWorkout(svc *Service) func(context.Context, *mcp.CallToolRequest, ScheduleWorkoutInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args ScheduleWorkoutInput) (*mcp.CallToolResult, any, error) {
		if args.WorkoutID == "" {
			return nil, nil, fmt.Errorf("workout_id is required")
		}
		if err := validateDate("date", args.Date, false); err != nil {
			return nil, nil, err
		}
		if args.DryRun {
			return mcputil.DataResult(map[string]any{
				"dry_run": true, "method": "POST",
				"path": "/workout-service/schedule/" + args.WorkoutID,
				"body": map[string]string{"date": args.Date},
			})
		}
		if _, err := svc.ScheduleWorkout(ctx, args.WorkoutID, args.Date); err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(map[string]any{
			"workout_id": args.WorkoutID, "date": args.Date, "scheduled": true,
		})
	}
}

func handleListWorkouts(svc *Service) func(context.Context, *mcp.CallToolRequest, ListWorkoutsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args ListWorkoutsInput) (*mcp.CallToolResult, any, error) {
		body, err := svc.ListWorkouts(ctx, args.Limit.Int())
		if err != nil {
			return nil, nil, err
		}
		return mcputil.JSONResult(body), nil, nil
	}
}

func handleGetWorkout(svc *Service) func(context.Context, *mcp.CallToolRequest, GetWorkoutInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args GetWorkoutInput) (*mcp.CallToolResult, any, error) {
		if args.WorkoutID == "" {
			return nil, nil, fmt.Errorf("workout_id is required")
		}
		body, err := svc.GetWorkout(ctx, args.WorkoutID)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.JSONResult(body), nil, nil
	}
}

func handleDeleteWorkout(svc *Service) func(context.Context, *mcp.CallToolRequest, DeleteWorkoutInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args DeleteWorkoutInput) (*mcp.CallToolResult, any, error) {
		if args.WorkoutID == "" {
			return nil, nil, fmt.Errorf("workout_id is required")
		}
		// Fetch-then-confirm: always return the current workout, only delete with
		// confirm=true (and not dry_run).
		current, err := svc.GetWorkout(ctx, args.WorkoutID)
		if err != nil {
			return nil, nil, err
		}
		if args.DryRun || !args.Confirm {
			reason := "confirm is false; pass confirm=true to delete"
			if args.DryRun {
				reason = "dry_run"
			}
			return mcputil.DataResult(map[string]any{
				"deleted": false, "reason": reason, "workout": jsonRaw(current),
			})
		}
		body, err := svc.DeleteWorkout(ctx, args.WorkoutID)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(map[string]any{
			"deleted": true, "workout_id": args.WorkoutID, "response": jsonRaw(body),
		})
	}
}

func handleGetCalendar(svc *Service) func(context.Context, *mcp.CallToolRequest, CalendarInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args CalendarInput) (*mcp.CallToolResult, any, error) {
		if args.Year < 2000 || args.Month < 1 || args.Month > 12 {
			return nil, nil, fmt.Errorf("year must be >= 2000 and month 1-12")
		}
		// Garmin's calendar-service month is 0-indexed (Jan=0); the tool takes a
		// human 1-indexed month.
		body, err := svc.GetCalendar(ctx, args.Year.Int(), args.Month.Int()-1)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.JSONResult(body), nil, nil
	}
}

// handleDateRead adapts a single-date Service read (daily summary, sleep, HRV,
// stress) into a tool handler, validating the date before the call.
func handleDateRead(read func(context.Context, string) ([]byte, error)) func(context.Context, *mcp.CallToolRequest, DateInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args DateInput) (*mcp.CallToolResult, any, error) {
		if err := validateDate("date", args.Date, false); err != nil {
			return nil, nil, err
		}
		body, err := read(ctx, args.Date)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.JSONResult(body), nil, nil
	}
}

func handleGetBodyBattery(svc *Service) func(context.Context, *mcp.CallToolRequest, DateRangeInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args DateRangeInput) (*mcp.CallToolResult, any, error) {
		if err := validateDate("start_date", args.StartDate, false); err != nil {
			return nil, nil, err
		}
		if err := validateDate("end_date", args.EndDate, false); err != nil {
			return nil, nil, err
		}
		body, err := svc.GetBodyBattery(ctx, args.StartDate, args.EndDate)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.JSONResult(body), nil, nil
	}
}

// jsonRaw wraps already-encoded JSON so it nests in a DataResult map without being
// re-stringified.
func jsonRaw(b []byte) json.RawMessage { return json.RawMessage(b) }
