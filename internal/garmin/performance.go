package garmin

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	performanceMetricsURL = connectAPIHost + "/metrics-service/metrics"
	lactateThresholdURL   = connectAPIHost + "/biometric-service/biometric/latestLactateThreshold"
)

type vo2MaxRecord struct {
	Date    string   `json:"calendarDate,omitempty"`
	Value   *float64 `json:"vo2MaxValue,omitempty"`
	Precise *float64 `json:"vo2MaxPreciseValue,omitempty"`
}

func registerPerformanceTools(server *mcp.Server, svc *Service) {
	for _, tool := range []struct {
		name, description, path string
	}{
		{"get_vo2_max", "Read the latest Garmin running and cycling VO2 max estimates as of date (YYYY-MM-DD).", "/maxmet/latest/"},
		{"get_training_status", "Read Garmin Training Status and acute/chronic training load for date (YYYY-MM-DD).", "/trainingstatus/daily/"},
		{"get_training_readiness", "Read Garmin Training Readiness scores and contributing factors recorded on date (YYYY-MM-DD). Multiple entries may be returned.", "/trainingreadiness/"},
	} {
		mcp.AddTool(server, &mcp.Tool{
			Name: tool.name, Description: tool.description,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in DateInput) (*mcp.CallToolResult, any, error) {
			if _, err := time.Parse(time.DateOnly, in.Date); err != nil {
				return nil, nil, fmt.Errorf("date must be a valid YYYY-MM-DD date")
			}
			body, err := svc.readPerformanceMetric(ctx, performanceMetricsURL+tool.path+in.Date)
			if err != nil {
				return nil, nil, err
			}
			switch tool.name {
			case "get_vo2_max":
				// The same Garmin response carries heat/altitude acclimation.
				var records struct {
					Running *vo2MaxRecord `json:"generic"`
					Cycling *vo2MaxRecord `json:"cycling"`
				}
				if err := json.Unmarshal(body, &records); err != nil {
					return nil, nil, fmt.Errorf("decode Garmin VO2 max records: %w", err)
				}
				return mcputil.DataResult(map[string]*vo2MaxRecord{"running": records.Running, "cycling": records.Cycling})
			case "get_training_status":
				var response struct {
					Records map[string]map[string]json.RawMessage `json:"latestTrainingStatusData"`
				}
				if err := json.Unmarshal(body, &response); err != nil {
					return nil, nil, fmt.Errorf("decode Garmin Training Status: %w", err)
				}
				records := make([]map[string]json.RawMessage, 0, len(response.Records))
				for _, key := range slices.Sorted(maps.Keys(response.Records)) {
					record := response.Records[key]
					delete(record, "deviceId")
					records = append(records, record)
				}
				return mcputil.DataResult(records)
			default:
				var records []map[string]json.RawMessage
				if err := json.Unmarshal(body, &records); err != nil {
					return nil, nil, fmt.Errorf("decode Garmin Training Readiness: %w", err)
				}
				for _, record := range records {
					delete(record, "userProfilePK")
					delete(record, "deviceId")
				}
				return mcputil.DataResult(records)
			}
		})
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_lactate_threshold",
		Description: "Read Garmin's latest running lactate threshold estimate, including heart rate and speed when available.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		body, err := svc.readPerformanceMetric(ctx, lactateThresholdURL)
		if err != nil {
			return nil, nil, err
		}
		type thresholdRecord struct {
			Date      string   `json:"calendarDate,omitempty"`
			Speed     *float64 `json:"speed,omitempty"`
			HeartRate *float64 `json:"heartRate,omitempty"`
			LegacyHR  *float64 `json:"hearRate,omitempty"`
		}
		var records []thresholdRecord
		if err := json.Unmarshal(body, &records); err != nil {
			return nil, nil, fmt.Errorf("decode Garmin lactate threshold: %w", err)
		}
		out := make([]map[string]any, 0, len(records))
		for _, record := range records {
			if record.HeartRate == nil {
				record.HeartRate = record.LegacyHR
			}
			if record.Speed != nil || record.HeartRate != nil {
				out = append(out, map[string]any{
					"date": record.Date, "speed_mps": record.Speed, "heart_rate_bpm": record.HeartRate,
				})
			}
		}
		return mcputil.DataResult(out)
	})
}

func (s *Service) readPerformanceMetric(ctx context.Context, target string) ([]byte, error) {
	bearer, err := s.bearer(ctx)
	if err != nil {
		return nil, err
	}
	body, err := s.client.authedJSON(ctx, bearer, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return []byte("null"), nil
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("Garmin returned an invalid performance metric response")
	}
	return body, nil
}
