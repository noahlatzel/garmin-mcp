package garmin

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

type healthDay struct {
	Date                  string   `json:"date"`
	SleepSeconds          *int     `json:"sleep_seconds,omitempty"`
	SleepScore            *int     `json:"sleep_score,omitempty"`
	HRVLastNightMS        *float64 `json:"hrv_last_night_ms,omitempty"`
	HRVWeeklyMS           *float64 `json:"hrv_weekly_ms,omitempty"`
	HRVStatus             string   `json:"hrv_status,omitempty"`
	RestingHeartRateBPM   *int     `json:"resting_heart_rate_bpm,omitempty"`
	AverageStress         *int     `json:"average_stress_score,omitempty"`
	BodyBatteryHigh       *int     `json:"body_battery_high_score,omitempty"`
	BodyBatteryLow        *int     `json:"body_battery_low_score,omitempty"`
	TotalExpenditureKcal  *float64 `json:"total_expenditure_kcal,omitempty"`
	ActiveExpenditureKcal *float64 `json:"active_expenditure_kcal,omitempty"`
	WeightKG              *float64 `json:"weight_kg,omitempty"`
	Weight7DayMeanKG      *float64 `json:"weight_7_day_mean_kg,omitempty"`
	Weight7DaySamples     int      `json:"weight_7_day_samples,omitempty"`
	ReadinessScore        *int     `json:"training_readiness_score,omitempty"`
	ReadinessMeasuredAt   string   `json:"training_readiness_measured_at,omitempty"`
}

type healthSummaryResult struct {
	Days   []healthDay     `json:"days"`
	Issues []coachingIssue `json:"issues,omitempty"`
}

func (s *Service) healthSummary(ctx context.Context, r coachingRange) (healthSummaryResult, error) {
	dates, err := r.dates(42)
	if err != nil {
		return healthSummaryResult{}, err
	}
	bearer, err := s.bearer(ctx)
	if err != nil {
		return healthSummaryResult{}, err
	}
	result := healthSummaryResult{Days: make([]healthDay, len(dates))}
	byDate := make(map[string]*healthDay, len(dates))
	for i, date := range dates {
		result.Days[i].Date = date
		byDate[date] = &result.Days[i]
	}
	rangeQuery := "?" + (url.Values{"startDate": {r.Start}, "endDate": {r.End}}).Encode()
	pathRange := "/" + r.Start + "/" + r.End
	read := func(source, endpoint string, dst any) bool {
		if err := s.readActivityJSON(ctx, bearer, endpoint, dst); err != nil {
			result.Issues = append(result.Issues, coachingIssue{Source: source, Error: err.Error()})
			return false
		}
		return true
	}
	var daily struct {
		Data []struct {
			Date      string `json:"calendarDate"`
			HeartRate struct {
				Resting *int `json:"restingValue"`
			} `json:"heartRate"`
			Stress struct {
				Average *int `json:"avgLevel"`
			} `json:"stress"`
			Battery struct {
				High *int `json:"maxValue"`
				Low  *int `json:"minValue"`
			} `json:"bodyBattery"`
			Calories struct {
				Total  *float64 `json:"burnedTotal"`
				Active *float64 `json:"burnedActive"`
			} `json:"calories"`
		} `json:"data"`
	}
	if read("daily_summary", connectAPIHost+"/usersummary-service/v2/dailysummaries"+rangeQuery, &daily) {
		for _, v := range daily.Data {
			if d := byDate[v.Date]; d != nil {
				d.RestingHeartRateBPM = v.HeartRate.Resting
				if v.Stress.Average != nil && *v.Stress.Average >= 0 {
					d.AverageStress = v.Stress.Average
				}
				d.BodyBatteryHigh, d.BodyBatteryLow = v.Battery.High, v.Battery.Low
				d.TotalExpenditureKcal, d.ActiveExpenditureKcal = v.Calories.Total, v.Calories.Active
			}
		}
	}
	var hrv struct {
		Summaries []struct {
			Date    string   `json:"calendarDate"`
			Nightly *float64 `json:"lastNightAvg"`
			Weekly  *float64 `json:"weeklyAvg"`
			Status  string   `json:"status"`
		} `json:"hrvSummaries"`
	}
	if read("hrv", hrvHost+"/daily"+pathRange, &hrv) {
		for _, v := range hrv.Summaries {
			if d := byDate[v.Date]; d != nil {
				d.HRVLastNightMS, d.HRVWeeklyMS, d.HRVStatus = v.Nightly, v.Weekly, v.Status
			}
		}
	}
	var readiness []struct {
		Date      string `json:"calendarDate"`
		Timestamp string `json:"timestamp"`
		Score     *int   `json:"score"`
	}
	if read("training_readiness", performanceMetricsURL+"/trainingreadiness"+pathRange, &readiness) {
		for _, v := range readiness {
			if d := byDate[v.Date]; d != nil && v.Score != nil && v.Timestamp >= d.ReadinessMeasuredAt {
				d.ReadinessScore, d.ReadinessMeasuredAt = v.Score, v.Timestamp
			}
		}
	}
	var weights struct {
		Entries []struct {
			Date       string          `json:"calendarDate"`
			Timestamp  json.RawMessage `json:"timestampGMT"`
			RecordedAt json.RawMessage `json:"date"`
			Weight     *float64        `json:"weight"`
		} `json:"dateWeightList"`
	}
	if read("weight", connectAPIHost+"/weight-service/weight/daterangesnapshot"+rangeQuery, &weights) {
		last := map[string]int64{}
		for _, v := range weights.Entries {
			timestamp := max(coachingWeightTimestamp(v.Timestamp), coachingWeightTimestamp(v.RecordedAt))
			if d := byDate[v.Date]; d != nil && v.Weight != nil && *v.Weight > 0 && (d.WeightKG == nil || timestamp > last[v.Date]) {
				d.WeightKG = new(*v.Weight / 1000)
				last[v.Date] = timestamp
			}
		}
	}
	for i := range result.Days {
		var sum float64
		var samples int
		for j := max(0, i-6); j <= i; j++ {
			if weight := result.Days[j].WeightKG; weight != nil {
				sum += *weight
				samples++
			}
		}
		if samples > 0 {
			result.Days[i].Weight7DayMeanKG = new(sum / float64(samples))
			result.Days[i].Weight7DaySamples = samples
		}
	}

	var sleep []coachingSleepRecord
	err = s.readActivityJSON(ctx, bearer, connectAPIHost+"/sleep-service/sleep/dailySleepSummariesByDate"+rangeQuery, &sleep)
	if err == nil {
		for _, v := range sleep {
			if d := byDate[v.Date]; d != nil {
				v.apply(d)
			}
		}
		return result, ctx.Err()
	}

	displayName, err := s.displayName(ctx, bearer)
	if err != nil {
		result.Issues = append(result.Issues, coachingIssue{Source: "sleep", Error: err.Error()})
		return result, ctx.Err()
	}
	sleepIssues := make([]*coachingIssue, len(dates))
	coachingParallel(len(dates), func(i int) {
		date := dates[i]
		body, err := s.client.GetSleep(ctx, bearer, displayName, date)
		if err == nil && len(body) > 0 {
			err = readCoachingSleep(body, &result.Days[i])
		}
		if err != nil && !strings.Contains(err.Error(), "(HTTP 404)") {
			sleepIssues[i] = &coachingIssue{Source: "sleep", Date: date, Error: err.Error()}
		}
	})
	for _, issue := range sleepIssues {
		if issue != nil {
			result.Issues = append(result.Issues, *issue)
		}
	}
	return result, ctx.Err()
}

func coachingWeightTimestamp(raw json.RawMessage) int64 {
	var millis int64
	if json.Unmarshal(raw, &millis) == nil {
		return millis
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
			if stamp, err := time.Parse(layout, text); err == nil {
				return stamp.UnixMilli()
			}
		}
	}
	return 0
}

type coachingSleepRecord struct {
	Date    string `json:"calendarDate"`
	Seconds *int   `json:"sleepTimeSeconds"`
	Scores  struct {
		Overall struct {
			Value *int `json:"value"`
		} `json:"overall"`
	} `json:"sleepScores"`
}

func (s coachingSleepRecord) apply(day *healthDay) {
	day.SleepSeconds, day.SleepScore = s.Seconds, s.Scores.Overall.Value
}

func readCoachingSleep(body []byte, day *healthDay) error {
	var response struct {
		Sleep coachingSleepRecord `json:"dailySleepDTO"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	response.Sleep.apply(day)
	return nil
}
