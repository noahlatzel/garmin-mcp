package garmin

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type coachingRange struct {
	Start string `json:"start" jsonschema:"First local calendar date, inclusive (YYYY-MM-DD)."`
	End   string `json:"end" jsonschema:"Last local calendar date, inclusive (YYYY-MM-DD)."`
}

func (r coachingRange) dates(maxDays int) ([]string, error) {
	start, startErr := time.Parse(time.DateOnly, r.Start)
	end, endErr := time.Parse(time.DateOnly, r.End)
	if startErr != nil || endErr != nil || end.Before(start) || end.Sub(start) >= time.Duration(maxDays)*24*time.Hour {
		return nil, fmt.Errorf("provide an inclusive YYYY-MM-DD range of 1 to %d days", maxDays)
	}
	var days []string
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		days = append(days, day.Format(time.DateOnly))
	}
	return days, nil
}

type coachingIssue struct {
	Source string `json:"source"`
	Date   string `json:"date,omitempty"`
	ID     int64  `json:"activity_id,omitempty"`
	Error  string `json:"error"`
}

func registerCoachingTools(server *mcp.Server, svc *Service) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_health_summary", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		Description: "Read up to 42 days of compact daily recovery data. Sleep is in seconds, HRV in milliseconds, resting heart rate in bpm, energy expenditure in kcal, weight in kg, and stress, Body Battery, sleep and readiness are Garmin scores. Includes the last weigh-in per day and its trailing seven-day average within the requested range. Missing measurements are omitted; source failures are listed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in coachingRange) (*mcp.CallToolResult, any, error) {
		result, err := svc.healthSummary(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(result)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_training_summary", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		Description: "Read up to 56 days of completed training, grouped by Monday-start week and Garmin sport. Counts are sessions, distances/elevation in metres, durations and recorded heart-rate zone times in seconds, speed in m/s, running pace in seconds/km, heart rate in bpm, power in watts, and training load in unitless Garmin scores. Averages are duration-weighted over available samples. Zone numbers use each activity's recorded Garmin zones. Includes acute/chronic load snapshots when available and reports partial weeks.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in trainingSummaryInput) (*mcp.CallToolResult, any, error) {
		result, err := svc.trainingSummary(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(result)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_strength_progress", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		Description: "Read up to 56 days of recorded Garmin strength sessions and per-exercise weekly progress. Active sets and reps are counts, duration is in seconds, external load in kg, and recorded volume is kg × reps. Sets without both weight and reps are excluded from volume. Use include_sets for per-set details.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in strengthProgressInput) (*mcp.CallToolResult, any, error) {
		result, err := svc.strengthProgress(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(result)
	})
}

// Garmin may limit individual requests. Keep fan-out small and write results
// back by index so response order is independent of request completion order.
func coachingParallel(count int, work func(int)) {
	var group sync.WaitGroup
	for worker := range min(4, count) {
		group.Go(func() {
			for index := worker; index < count; index += 4 {
				work(index)
			}
		})
	}
	group.Wait()
}

func coachingWeek(date string) string {
	day, _ := time.Parse(time.DateOnly, date)
	offset := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -offset).Format(time.DateOnly)
}

func (s *Service) coachingActivities(ctx context.Context, r coachingRange, sport string) ([]listedActivity, error) {
	var all []listedActivity
	for start := 0; start < 500; start += maxActivityPage {
		page, err := s.listActivities(ctx, activityListInput{
			ActivityType: sport, StartDate: r.Start, EndDate: r.End,
			Start: mcputil.FlexInt(start), Limit: new(mcputil.FlexInt(maxActivityPage)),
		})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < maxActivityPage {
			return all, nil
		}
	}
	return nil, fmt.Errorf("activity range reached the 500-session limit; request a shorter range")
}

func coachingMapValues[K ~string | ~int, V any](records map[K]V) []V {
	out := make([]V, 0, len(records))
	for _, key := range slices.Sorted(maps.Keys(records)) {
		out = append(out, records[key])
	}
	return out
}

func coachingActivityURL(id int64, suffix string) string {
	return activityURL + "/" + strconv.FormatInt(id, 10) + "/" + suffix
}
