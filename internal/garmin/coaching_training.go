package garmin

import (
	"context"
	"strings"
	"time"

	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
)

type trainingSummaryInput struct {
	coachingRange
	IncludeHRZones *mcputil.FlexBool `json:"include_hr_zones,omitempty" jsonschema:"Include recorded heart-rate zone durations. Defaults to true; turn off for a faster volume-only summary."`
}

type trainingWeek struct {
	WeekStart string                `json:"week_start"`
	From      string                `json:"from"`
	Through   string                `json:"through"`
	Days      int                   `json:"days_in_requested_range"`
	Sports    []*trainingSport      `json:"sports"`
	Load      *coachingTrainingLoad `json:"load_snapshot,omitempty"`
}

type coachingTrainingLoad struct {
	Date     string   `json:"date"`
	Feedback string   `json:"status_feedback,omitempty"`
	Acute    *float64 `json:"acute_load_score,omitempty"`
	Chronic  *float64 `json:"chronic_load_score,omitempty"`
	Ratio    *float64 `json:"acute_chronic_ratio,omitempty"`
}

type garminHRZone struct {
	Zone    int      `json:"zoneNumber"`
	Seconds *float64 `json:"secsInZone"`
	Millis  *float64 `json:"millisInZone"`
}
type coachingZone struct {
	Zone    int     `json:"zone_number"`
	Seconds float64 `json:"seconds"`
}

type trainingSport struct {
	Sport            string         `json:"sport"`
	Sessions         int            `json:"sessions"`
	Distance         *float64       `json:"distance_m,omitempty"`
	Duration         *float64       `json:"duration_seconds,omitempty"`
	Elevation        *float64       `json:"elevation_gain_m,omitempty"`
	Speed            *float64       `json:"average_speed_mps,omitempty"`
	Pace             *float64       `json:"running_pace_seconds_per_km,omitempty"`
	HR               *float64       `json:"average_heart_rate_bpm,omitempty"`
	Power            *float64       `json:"average_power_watts,omitempty"`
	Load             *float64       `json:"activity_training_load_score,omitempty"`
	ZoneSessions     int            `json:"sessions_with_hr_zones"`
	Zones            []coachingZone `json:"heart_rate_zones,omitempty"`
	hr, power, speed weightedMetric
	zones            map[int]float64
}

type weightedMetric struct{ sum, seconds float64 }

func (m *weightedMetric) add(value *float64, seconds float64) {
	if value != nil && *value > 0 && seconds > 0 {
		m.sum += *value * seconds
		m.seconds += seconds
	}
}

func (m weightedMetric) average() *float64 {
	if m.seconds == 0 {
		return nil
	}
	return new(m.sum / m.seconds)
}

func addCoachingMetric(total **float64, sample *float64) {
	if sample == nil {
		return
	}
	if *total == nil {
		*total = new(0.0)
	}
	**total += *sample
}

type trainingSummaryResult struct {
	Weeks  []*trainingWeek `json:"weeks"`
	Issues []coachingIssue `json:"issues,omitempty"`
}

func (s *Service) trainingSummary(ctx context.Context, in trainingSummaryInput) (trainingSummaryResult, error) {
	dates, err := in.dates(56)
	if err != nil {
		return trainingSummaryResult{}, err
	}
	activities, err := s.coachingActivities(ctx, in.coachingRange, "")
	if err != nil {
		return trainingSummaryResult{}, err
	}
	bearer, err := s.bearer(ctx)
	if err != nil {
		return trainingSummaryResult{}, err
	}
	weeks := map[string]*trainingWeek{}
	sports := map[string]map[string]*trainingSport{}
	for _, date := range dates {
		key := coachingWeek(date)
		if weeks[key] == nil {
			weeks[key] = &trainingWeek{WeekStart: key, From: date, Sports: []*trainingSport{}}
			sports[key] = map[string]*trainingSport{}
		}
		weeks[key].Through = date
		weeks[key].Days++
	}
	result := trainingSummaryResult{Weeks: coachingMapValues(weeks)}
	targets := make([]*trainingSport, len(activities))
	for i, a := range activities {
		if len(a.StartTime) < 10 {
			continue
		}
		date := a.StartTime[:10]
		if _, err := time.Parse(time.DateOnly, date); err != nil || date < in.Start || date > in.End {
			continue
		}
		key := coachingWeek(date)
		sport := a.Type.TypeKey
		if sport == "" {
			sport = "unknown"
		}
		group := sports[key][sport]
		if group == nil {
			group = &trainingSport{Sport: sport, zones: map[int]float64{}}
			sports[key][sport] = group
		}
		targets[i] = group
		group.Sessions++
		addCoachingMetric(&group.Distance, a.Distance)
		addCoachingMetric(&group.Duration, a.Duration)
		addCoachingMetric(&group.Elevation, a.ElevationGain)
		addCoachingMetric(&group.Load, a.TrainingLoad)
		if a.Duration != nil {
			group.hr.add(a.AverageHR, *a.Duration)
			group.power.add(a.AveragePower, *a.Duration)
			speedSeconds := *a.Duration
			if a.MovingDuration != nil && *a.MovingDuration > 0 {
				speedSeconds = *a.MovingDuration
			}
			group.speed.add(a.AverageSpeed, speedSeconds)
		}
	}

	if in.IncludeHRZones == nil || in.IncludeHRZones.Bool() {
		if len(activities) > 120 {
			result.Issues = append(result.Issues, coachingIssue{Source: "heart_rate_zones", Error: "more than 120 activities; request a shorter range for zones"})
		} else {
			zones := make([][]garminHRZone, len(activities))
			issues := make([]*coachingIssue, len(activities))
			coachingParallel(len(activities), func(i int) {
				a := activities[i]
				if targets[i] == nil || a.ID <= 0 || a.AverageHR == nil || *a.AverageHR <= 0 {
					return
				}
				if err := s.readActivityJSON(ctx, bearer, coachingActivityURL(a.ID, "hrTimeInZones"), &zones[i]); err != nil {
					issues[i] = &coachingIssue{Source: "heart_rate_zones", ID: a.ID, Error: err.Error()}
				}
			})
			for i, series := range zones {
				if issues[i] != nil {
					result.Issues = append(result.Issues, *issues[i])
				}
				hasZones := false
				for _, z := range series {
					switch {
					case z.Zone <= 0:
						continue
					case z.Seconds != nil && *z.Seconds >= 0:
						targets[i].zones[z.Zone] += *z.Seconds
					case z.Millis != nil && *z.Millis >= 0:
						targets[i].zones[z.Zone] += *z.Millis / 1000
					default:
						continue
					}
					hasZones = true
				}
				if hasZones {
					targets[i].ZoneSessions++
				}
			}
		}
	}

	for _, week := range result.Weeks {
		week.Sports = coachingMapValues(sports[week.WeekStart])
		for _, group := range week.Sports {
			group.HR, group.Power, group.Speed = group.hr.average(), group.power.average(), group.speed.average()
			if group.Speed != nil && strings.Contains(group.Sport, "running") {
				group.Pace = new(1000 / *group.Speed)
			}
			byZone := map[int]coachingZone{}
			for zone, seconds := range group.zones {
				byZone[zone] = coachingZone{Zone: zone, Seconds: seconds}
			}
			group.Zones = coachingMapValues(byZone)
		}
		load, err := s.coachingLoadSnapshot(ctx, bearer, week.Through)
		if err != nil {
			result.Issues = append(result.Issues, coachingIssue{Source: "training_load", Date: week.Through, Error: err.Error()})
		}
		week.Load = load
	}
	return result, ctx.Err()
}

func (s *Service) coachingLoadSnapshot(ctx context.Context, bearer, date string) (*coachingTrainingLoad, error) {
	var response struct {
		Records map[string]struct {
			Date     string `json:"calendarDate"`
			Primary  bool   `json:"primaryTrainingDevice"`
			Feedback string `json:"trainingStatusFeedbackPhrase"`
			Load     struct {
				Acute   *float64 `json:"dailyTrainingLoadAcute"`
				Chronic *float64 `json:"dailyTrainingLoadChronic"`
				Ratio   *float64 `json:"dailyAcuteChronicWorkloadRatio"`
			} `json:"acuteTrainingLoadDTO"`
		} `json:"latestTrainingStatusData"`
	}
	err := s.readActivityJSON(ctx, bearer, performanceMetricsURL+"/trainingstatus/daily/"+date, &response)
	if err != nil {
		return nil, err
	}
	var out *coachingTrainingLoad
	for _, v := range coachingMapValues(response.Records) {
		if v.Date == "" || v.Date > date {
			continue
		}
		if out == nil || v.Primary || v.Date > out.Date {
			out = &coachingTrainingLoad{Date: v.Date, Feedback: v.Feedback, Acute: v.Load.Acute, Chronic: v.Load.Chronic, Ratio: v.Load.Ratio}
		}
		if v.Primary {
			break
		}
	}
	return out, nil
}
