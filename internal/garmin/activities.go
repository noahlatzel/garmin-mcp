package garmin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	activitySearchURL = connectAPIHost + "/activitylist-service/activities/search/activities"
	activityURL       = connectAPIHost + "/activity-service/activity"
	maxActivityPage   = 50
	maxActivityLaps   = 100
)

type activityListInput struct {
	ActivityType string           `json:"activity_type,omitempty" jsonschema:"Garmin activity type, for example running or cycling. Omit for all sports."`
	StartDate    string           `json:"start_date,omitempty" jsonschema:"First local activity date, YYYY-MM-DD, inclusive."`
	EndDate      string           `json:"end_date,omitempty" jsonschema:"Last local activity date, YYYY-MM-DD, inclusive."`
	Start        mcputil.FlexInt  `json:"start,omitempty" jsonschema:"Zero-based pagination offset. Default 0."`
	Limit        *mcputil.FlexInt `json:"limit,omitempty" jsonschema:"Page size, 1 to 50. Default 20."`
}

type activityDetailInput struct {
	ActivityID  mcputil.FlexInt  `json:"activity_id" jsonschema:"Positive activity ID returned by list_activities."`
	IncludeLaps mcputil.FlexBool `json:"include_laps,omitempty" jsonschema:"Include up to 100 recorded laps. Default false."`
}

// Only training-related fields are decoded and returned. Garmin uses metres,
// seconds, metres per second, bpm and watts for the corresponding metrics.
type activityMetrics struct {
	Distance              *float64 `json:"distance,omitempty"`
	Duration              *float64 `json:"duration,omitempty"`
	MovingDuration        *float64 `json:"movingDuration,omitempty"`
	AverageSpeed          *float64 `json:"averageSpeed,omitempty"`
	AverageHR             *float64 `json:"averageHR,omitempty"`
	MaxHR                 *float64 `json:"maxHR,omitempty"`
	ElevationGain         *float64 `json:"elevationGain,omitempty"`
	ElevationLoss         *float64 `json:"elevationLoss,omitempty"`
	AveragePower          *float64 `json:"averagePower,omitempty"`
	NormalizedPower       *float64 `json:"normPower,omitempty"`
	AerobicTrainingEffect *float64 `json:"aerobicTrainingEffect,omitempty"`
	AnaerobicEffect       *float64 `json:"anaerobicTrainingEffect,omitempty"`
	TrainingLoad          *float64 `json:"activityTrainingLoad,omitempty"`
}

type activityType struct {
	TypeKey string `json:"typeKey"`
}

type listedActivity struct {
	ID        int64        `json:"activityId"`
	Name      string       `json:"activityName"`
	Type      activityType `json:"activityType"`
	StartTime string       `json:"startTimeLocal"`
	activityMetrics
}

type activityLap struct {
	Index int `json:"lapIndex"`
	activityMetrics
}

type activityDetails struct {
	ID      int64        `json:"activityId"`
	Name    string       `json:"activityName"`
	Type    activityType `json:"activityTypeDTO"`
	Summary struct {
		StartTime string `json:"startTimeLocal"`
		activityMetrics
	} `json:"summaryDTO"`
	Laps          *[]activityLap `json:"laps,omitempty"`
	LapsTruncated bool           `json:"lapsTruncated,omitempty"`
}

func registerActivityTools(server *mcp.Server, svc *Service) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_activities",
		Description: "List completed Garmin activities, newest first, with optional sport and date filters. " +
			"Returns activity IDs and training metrics. Units: metres, seconds, metres per second, bpm and watts.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema: mcputil.InputSchema[activityListInput]("list_activities", "start", "limit"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in activityListInput) (*mcp.CallToolResult, any, error) {
		activities, err := svc.listActivities(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(activities)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_activity_details",
		Description: "Read one completed Garmin activity by ID. Returns training metrics and optionally " +
			"recorded laps when available. Units: metres, seconds, metres per second, bpm and watts.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema: mcputil.InputSchema[activityDetailInput]("get_activity_details", "activity_id", "include_laps"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in activityDetailInput) (*mcp.CallToolResult, any, error) {
		details, err := svc.activityDetails(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return mcputil.DataResult(details)
	})
}

func (in activityListInput) query() (url.Values, error) {
	limit := 20
	if in.Limit != nil {
		limit = in.Limit.Int()
	}
	if in.Start.Int() < 0 || limit < 1 || limit > maxActivityPage {
		return nil, fmt.Errorf("start must be non-negative and limit must be between 1 and %d", maxActivityPage)
	}
	query := url.Values{"start": {strconv.Itoa(in.Start.Int())}, "limit": {strconv.Itoa(limit)}}
	for _, date := range []struct{ key, value string }{
		{"startDate", in.StartDate},
		{"endDate", in.EndDate},
	} {
		if date.value != "" {
			if _, err := time.Parse(time.DateOnly, date.value); err != nil {
				return nil, fmt.Errorf("%s must be a valid YYYY-MM-DD date", date.key)
			}
			query.Set(date.key, date.value)
		}
	}
	if in.StartDate != "" && in.EndDate != "" && in.StartDate > in.EndDate {
		return nil, fmt.Errorf("start_date must be on or before end_date")
	}
	if in.ActivityType != "" {
		query.Set("activityType", in.ActivityType)
	}
	return query, nil
}

func (s *Service) listActivities(ctx context.Context, in activityListInput) ([]listedActivity, error) {
	query, err := in.query()
	if err != nil {
		return nil, err
	}
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	activities := make([]listedActivity, 0)
	if err := s.readActivityJSON(ctx, bearer, activitySearchURL+"?"+query.Encode(), &activities); err != nil {
		return nil, err
	}
	if activities == nil {
		activities = []listedActivity{}
	}
	return activities, nil
}

func (s *Service) activityDetails(ctx context.Context, in activityDetailInput) (activityDetails, error) {
	var details activityDetails
	if in.ActivityID.Int() <= 0 {
		return details, fmt.Errorf("activity_id must be a positive integer")
	}
	bearer, err := s.bearer(ctx)
	if err != nil {
		return details, err
	}
	target := activityURL + "/" + strconv.Itoa(in.ActivityID.Int())
	if err := s.readActivityJSON(ctx, bearer, target, &details); err != nil {
		return details, err
	}
	if in.IncludeLaps.Bool() {
		var response struct {
			Laps []activityLap `json:"lapDTOs"`
		}
		response.Laps = make([]activityLap, 0)
		if err := s.readActivityJSON(ctx, bearer, target+"/laps", &response); err != nil {
			return details, err
		}
		details.LapsTruncated = len(response.Laps) > maxActivityLaps
		response.Laps = response.Laps[:min(len(response.Laps), maxActivityLaps)]
		if response.Laps == nil {
			response.Laps = []activityLap{}
		}
		details.Laps = &response.Laps
	}
	return details, nil
}

func (s *Service) readActivityJSON(ctx context.Context, bearer, target string, result any) error {
	body, err := s.client.authedJSON(ctx, bearer, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode Garmin activity response: %w", err)
	}
	return nil
}
