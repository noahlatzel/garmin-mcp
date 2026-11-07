package garmin

import (
	"context"
	"strings"
	"time"

	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
)

type strengthProgressInput struct {
	coachingRange
	IncludeSets mcputil.FlexBool `json:"include_sets,omitempty" jsonschema:"Include individual active sets in addition to weekly exercise totals. Default false."`
}

type recordedSet struct {
	Category string   `json:"category,omitempty"`
	Exercise string   `json:"exercise,omitempty"`
	Reps     *int     `json:"reps,omitempty"`
	Weight   *float64 `json:"external_weight_kg,omitempty"`
	Duration *float64 `json:"duration_seconds,omitempty"`
}

type strengthTotals struct {
	ActiveSets   int      `json:"active_sets"`
	SetsWithReps int      `json:"sets_with_recorded_reps"`
	Reps         *int     `json:"recorded_reps,omitempty"`
	VolumeSets   int      `json:"sets_with_recorded_weight_and_reps"`
	Volume       *float64 `json:"recorded_volume_kg_reps,omitempty"`
	Heaviest     *float64 `json:"heaviest_external_weight_kg,omitempty"`
}

func (t *strengthTotals) add(set recordedSet) {
	t.ActiveSets++
	if set.Reps != nil {
		t.SetsWithReps++
		if t.Reps == nil {
			t.Reps = new(0)
		}
		*t.Reps += *set.Reps
	}
	if set.Weight != nil {
		if t.Heaviest == nil || *set.Weight > *t.Heaviest {
			t.Heaviest = set.Weight
		}
		if set.Reps != nil {
			t.VolumeSets++
			addCoachingMetric(&t.Volume, new(*set.Weight*float64(*set.Reps)))
		}
	}
}

type strengthSession struct {
	ID            int64    `json:"activity_id"`
	Date          string   `json:"date"`
	Name          string   `json:"name,omitempty"`
	Duration      *float64 `json:"duration_seconds,omitempty"`
	SetsAvailable bool     `json:"sets_available"`
	strengthTotals
	Sets []recordedSet `json:"sets,omitempty"`
}

type strengthExerciseWeek struct {
	WeekStart string `json:"week_start"`
	Category  string `json:"category,omitempty"`
	Exercise  string `json:"exercise,omitempty"`
	strengthTotals
}

type strengthProgressResult struct {
	Sessions      []strengthSession       `json:"sessions"`
	ExerciseWeeks []*strengthExerciseWeek `json:"exercise_weeks"`
	Issues        []coachingIssue         `json:"issues,omitempty"`
}

func (s *Service) strengthProgress(ctx context.Context, in strengthProgressInput) (strengthProgressResult, error) {
	if _, err := in.dates(56); err != nil {
		return strengthProgressResult{}, err
	}
	activities, err := s.coachingActivities(ctx, in.coachingRange, "")
	if err != nil {
		return strengthProgressResult{}, err
	}
	bearer, err := s.bearer(ctx)
	if err != nil {
		return strengthProgressResult{}, err
	}
	result := strengthProgressResult{Sessions: []strengthSession{}, ExerciseWeeks: []*strengthExerciseWeek{}}
	for _, a := range activities {
		if a.Type.TypeKey != "strength_training" || a.ID <= 0 || len(a.StartTime) < 10 {
			continue
		}
		date := a.StartTime[:10]
		if _, err := time.Parse(time.DateOnly, date); err != nil || date < in.Start || date > in.End {
			continue
		}
		result.Sessions = append(result.Sessions, strengthSession{ID: a.ID, Date: date, Name: a.Name, Duration: a.Duration})
	}
	if len(result.Sessions) > 80 {
		result.Issues = append(result.Issues, coachingIssue{Source: "exercise_sets", Error: "more than 80 strength sessions; request a shorter range for set details"})
		return result, nil
	}

	sets := make([][]recordedSet, len(result.Sessions))
	issues := make([]*coachingIssue, len(result.Sessions))
	coachingParallel(len(result.Sessions), func(i int) {
		session := result.Sessions[i]
		entries, err := s.readStrengthSets(ctx, bearer, session.ID)
		if err != nil {
			issues[i] = &coachingIssue{Source: "exercise_sets", Date: session.Date, ID: session.ID, Error: err.Error()}
			return
		}
		sets[i] = entries
	})
	weeks := map[string]*strengthExerciseWeek{}
	for i := range result.Sessions {
		session := &result.Sessions[i]
		if issues[i] != nil {
			result.Issues = append(result.Issues, *issues[i])
			continue
		}
		session.SetsAvailable = len(sets[i]) > 0
		if in.IncludeSets {
			session.Sets = sets[i]
		}
		week := coachingWeek(session.Date)
		for _, set := range sets[i] {
			session.add(set)
			if set.Exercise == "" && set.Category == "" {
				continue
			}
			key := week + "\x00" + set.Category + "\x00" + set.Exercise
			entry := weeks[key]
			if entry == nil {
				entry = &strengthExerciseWeek{WeekStart: week, Category: set.Category, Exercise: set.Exercise}
				weeks[key] = entry
			}
			entry.add(set)
		}
	}
	result.ExerciseWeeks = coachingMapValues(weeks)
	return result, ctx.Err()
}

func (s *Service) readStrengthSets(ctx context.Context, bearer string, id int64) ([]recordedSet, error) {
	var response struct {
		Sets []struct {
			Type      string   `json:"setType"`
			Reps      *int     `json:"repetitionCount"`
			Weight    *float64 `json:"weight"`
			Duration  *float64 `json:"duration"`
			Exercises []struct {
				Category    string `json:"category"`
				Name        string `json:"name"`
				SubCategory string `json:"subCategory"`
			} `json:"exercises"`
		} `json:"exerciseSets"`
	}
	if err := s.readActivityJSON(ctx, bearer, coachingActivityURL(id, "exerciseSets"), &response); err != nil {
		return nil, err
	}
	out := make([]recordedSet, 0)
	for _, v := range response.Sets {
		if v.Type != "ACTIVE" {
			continue
		}
		set := recordedSet{Duration: v.Duration}
		if v.Reps != nil && *v.Reps > 0 {
			set.Reps = v.Reps
		}
		if v.Weight != nil && *v.Weight >= 0 {
			set.Weight = new(*v.Weight / 1000)
		}
		if len(v.Exercises) > 0 {
			e := v.Exercises[0]
			if !strings.EqualFold(e.Category, "unknown") {
				set.Category = e.Category
			}
			set.Exercise = e.Name
			if set.Exercise == "" {
				set.Exercise = e.SubCategory
			}
			if strings.EqualFold(set.Exercise, "unknown") {
				set.Exercise = ""
			}
		}
		out = append(out, set)
	}
	return out, nil
}
