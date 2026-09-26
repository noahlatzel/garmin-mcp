package garmin

import (
	"fmt"
	"strconv"
	"strings"
)

// Garmin workout target enums, DRAFT — sourced from reference implementations;
// confirm against a manual harvest before trusting new values. no.target=1 is live-verified by the strength builder.
const (
	targetPowerZoneID  = 2
	targetPowerZoneKey = "power.zone"
	targetCadenceID    = 3
	targetCadenceKey   = "cadence.zone"
	targetHRZoneID     = 4
	targetHRZoneKey    = "heart.rate.zone"
	targetSpeedZoneID  = 5
	targetSpeedZoneKey = "speed.zone"
	targetPaceZoneID   = 6
	targetPaceZoneKey  = "pace.zone"
)

// Target kinds accepted by TargetSpec.Kind.
const (
	TargetNone      = ""
	TargetPace      = "pace"       // Low/High in m/s
	TargetHeartRate = "heart_rate" // Low/High in bpm, or Zone 1-5
	TargetPower     = "power"      // Low/High in watts, or Zone
	TargetCadence   = "cadence"    // Low/High in rpm/spm
	TargetSpeed     = "speed"      // Low/High in m/s
)

// TargetSpec is a sport-agnostic intensity target: either a numbered zone or a
// Low..High range in the kind's canonical unit (pace/speed m/s, heart rate bpm,
// power watts, cadence rpm). Zero value means no target.
type TargetSpec struct {
	Kind string
	Zone int
	Low  float64
	High float64
}

// allowedTargets lists the target kinds each sport's builder accepts.
var allowedTargets = map[string]map[string]bool{
	sportSwimKey: {TargetPace: true, TargetHeartRate: true},
}

// Validate checks the spec is internally consistent and its kind is allowed for
// the given sport key.
func (t TargetSpec) Validate(sportKey string) error {
	if t.Kind == TargetNone {
		if t.Zone != 0 || t.Low != 0 || t.High != 0 {
			return fmt.Errorf("target values given without a target kind")
		}
		return nil
	}
	if !allowedTargets[sportKey][t.Kind] {
		return fmt.Errorf("target kind %q is not supported for sport %q", t.Kind, sportKey)
	}
	hasZone := t.Zone != 0
	hasRange := t.Low != 0 || t.High != 0
	switch {
	case hasZone && hasRange:
		return fmt.Errorf("target %q: give a zone or a low/high range, not both", t.Kind)
	case hasZone:
		if t.Zone < 1 || t.Zone > 7 {
			return fmt.Errorf("target %q: zone %d out of range 1-7", t.Kind, t.Zone)
		}
	case hasRange:
		if t.Low <= 0 || t.High <= 0 {
			return fmt.Errorf("target %q: low and high must both be > 0", t.Kind)
		}
		if t.Low > t.High {
			return fmt.Errorf("target %q: low %g exceeds high %g", t.Kind, t.Low, t.High)
		}
	default:
		return fmt.Errorf("target %q has neither a zone nor a range", t.Kind)
	}
	return nil
}

// applySwim writes a swim target onto a step. Harvested contract: a swim pace
// target is a SECONDARY target with a single m/s value (the primary targetType
// stays no.target); a heart-rate target uses the primary slot (not yet
// harvested for swim — DRAFT).
func (t TargetSpec) applySwim(step *executableStep) {
	if t.Kind == TargetPace {
		noTarget := targetType{WorkoutTargetTypeID: targetNoTargetID, WorkoutTargetTypeKey: targetNoTargetKey}
		step.TargetType = noTarget
		step.SecondaryTargetType = &targetType{WorkoutTargetTypeID: targetPaceZoneID, WorkoutTargetTypeKey: targetPaceZoneKey}
		v := t.Low
		step.SecondaryTargetValueOne = &v
		return
	}
	t.apply(step)
}

// apply writes the target onto a step: the {id,key} targetType plus
// targetValueOne/targetValueTwo for a range or zoneNumber for a numbered zone.
// A zero spec applies no.target.
func (t TargetSpec) apply(step *executableStep) {
	if t.Kind == TargetNone {
		step.TargetType = targetType{WorkoutTargetTypeID: targetNoTargetID, WorkoutTargetTypeKey: targetNoTargetKey}
		return
	}
	var id int
	var key string
	switch t.Kind {
	case TargetPace:
		id, key = targetPaceZoneID, targetPaceZoneKey
	case TargetHeartRate:
		id, key = targetHRZoneID, targetHRZoneKey
	case TargetPower:
		id, key = targetPowerZoneID, targetPowerZoneKey
	case TargetCadence:
		id, key = targetCadenceID, targetCadenceKey
	case TargetSpeed:
		id, key = targetSpeedZoneID, targetSpeedZoneKey
	}
	step.TargetType = targetType{WorkoutTargetTypeID: id, WorkoutTargetTypeKey: key}
	if t.Zone > 0 {
		z := t.Zone
		step.ZoneNumber = &z
		return
	}
	low, high := t.Low, t.High
	step.TargetValueOne = &low
	step.TargetValueTwo = &high
}

// ParsePacePer100m converts a swim pace string over 100 m ("1:45", "105") to
// metres per second. Garmin serializes pace targets in m/s.
func ParsePacePer100m(pace string) (float64, error) {
	secs, err := parseClock(pace)
	if err != nil {
		return 0, fmt.Errorf("pace %q: %w (want M:SS per 100m, e.g. 1:45)", pace, err)
	}
	if secs <= 0 {
		return 0, fmt.Errorf("pace %q must be positive", pace)
	}
	return 100 / secs, nil
}

// parseClock parses "M:SS" (or a bare seconds count) into seconds.
func parseClock(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	parts := strings.Split(s, ":")
	if len(parts) > 2 {
		return 0, fmt.Errorf("too many ':' separators")
	}
	if len(parts) == 1 {
		return strconv.ParseFloat(parts[0], 64)
	}
	mins, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("minutes: %w", err)
	}
	secs, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, fmt.Errorf("seconds: %w", err)
	}
	if mins < 0 || secs < 0 || secs >= 60 {
		return 0, fmt.Errorf("minutes must be >= 0 and seconds in [0,60)")
	}
	return float64(mins)*60 + secs, nil
}
