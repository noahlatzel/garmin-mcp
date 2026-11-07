package garmin

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

type healthDay struct {
	Date                          string   `json:"date"`
	SleepSeconds                  *int     `json:"sleep_seconds,omitempty"`
	SleepScore                    *int     `json:"sleep_score,omitempty"`
	HRVLastNightMS                *float64 `json:"hrv_last_night_ms,omitempty"`
	HRVWeeklyMS                   *float64 `json:"hrv_weekly_ms,omitempty"`
	HRVStatus                     string   `json:"hrv_status,omitempty"`
	RestingHeartRateBPM           *int     `json:"resting_heart_rate_bpm,omitempty"`
	AverageStress                 *int     `json:"average_stress_score,omitempty"`
	BodyBatteryHigh               *int     `json:"body_battery_high_score,omitempty"`
	BodyBatteryLow                *int     `json:"body_battery_low_score,omitempty"`
	TotalExpenditureKcal          *float64 `json:"total_expenditure_kcal,omitempty"`
	ActiveExpenditureKcal         *float64 `json:"active_expenditure_kcal,omitempty"`
	WeightKG                      *float64 `json:"weight_kg,omitempty"`
	BMI                           *float64 `json:"bmi,omitempty"`
	BodyFatPercent                *float64 `json:"body_fat_percent,omitempty"`
	BodyWaterPercent              *float64 `json:"body_water_percent,omitempty"`
	SkeletalMuscleMassKG          *float64 `json:"skeletal_muscle_mass_kg,omitempty"`
	BoneMassKG                    *float64 `json:"bone_mass_kg,omitempty"`
	Weight7DayMeanKG              *float64 `json:"weight_7_day_mean_kg,omitempty"`
	Weight7DaySamples             int      `json:"weight_7_day_samples,omitempty"`
	BMI7DayMean                   *float64 `json:"bmi_7_day_mean,omitempty"`
	BMI7DaySamples                int      `json:"bmi_7_day_samples,omitempty"`
	BodyFat7DayMeanPercent        *float64 `json:"body_fat_7_day_mean_percent,omitempty"`
	BodyFat7DaySamples            int      `json:"body_fat_7_day_samples,omitempty"`
	BodyWater7DayMeanPercent      *float64 `json:"body_water_7_day_mean_percent,omitempty"`
	BodyWater7DaySamples          int      `json:"body_water_7_day_samples,omitempty"`
	SkeletalMuscleMass7DayMeanKG  *float64 `json:"skeletal_muscle_mass_7_day_mean_kg,omitempty"`
	SkeletalMuscleMass7DaySamples int      `json:"skeletal_muscle_mass_7_day_samples,omitempty"`
	BoneMass7DayMeanKG            *float64 `json:"bone_mass_7_day_mean_kg,omitempty"`
	BoneMass7DaySamples           int      `json:"bone_mass_7_day_samples,omitempty"`
	ReadinessScore                *int     `json:"training_readiness_score,omitempty"`
	ReadinessMeasuredAt           string   `json:"training_readiness_measured_at,omitempty"`
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
			BMI        *float64        `json:"bmi"`
			BodyFat    *float64        `json:"bodyFat"`
			BodyWater  *float64        `json:"bodyWater"`
			MuscleMass *float64        `json:"muscleMass"`
			BoneMass   *float64        `json:"boneMass"`
		} `json:"dateWeightList"`
	}
	if read("weight", connectAPIHost+"/weight-service/weight/daterangesnapshot"+rangeQuery, &weights) {
		last := map[string]int64{}
		for _, v := range weights.Entries {
			timestamp := max(coachingWeightTimestamp(v.Timestamp), coachingWeightTimestamp(v.RecordedAt))
			if d := byDate[v.Date]; d != nil && v.Weight != nil && *v.Weight > 0 && (d.WeightKG == nil || timestamp > last[v.Date]) {
				d.WeightKG = new(*v.Weight / 1000)
				d.BMI, d.BodyFatPercent, d.BodyWaterPercent = v.BMI, v.BodyFat, v.BodyWater
				d.SkeletalMuscleMassKG = optionalMassKG(v.MuscleMass)
				d.BoneMassKG = optionalMassKG(v.BoneMass)
				last[v.Date] = timestamp
			}
		}
	}
	for i := range result.Days {
		day := &result.Days[i]
		window := result.Days[max(0, i-6) : i+1]
		day.Weight7DayMeanKG, day.Weight7DaySamples = healthMean(window, func(d healthDay) *float64 { return d.WeightKG })
		day.BMI7DayMean, day.BMI7DaySamples = healthMean(window, func(d healthDay) *float64 { return d.BMI })
		day.BodyFat7DayMeanPercent, day.BodyFat7DaySamples = healthMean(window, func(d healthDay) *float64 { return d.BodyFatPercent })
		day.BodyWater7DayMeanPercent, day.BodyWater7DaySamples = healthMean(window, func(d healthDay) *float64 { return d.BodyWaterPercent })
		day.SkeletalMuscleMass7DayMeanKG, day.SkeletalMuscleMass7DaySamples = healthMean(window, func(d healthDay) *float64 { return d.SkeletalMuscleMassKG })
		day.BoneMass7DayMeanKG, day.BoneMass7DaySamples = healthMean(window, func(d healthDay) *float64 { return d.BoneMassKG })
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

func healthMean(days []healthDay, measurement func(healthDay) *float64) (*float64, int) {
	var sum float64
	var samples int
	for _, day := range days {
		if value := measurement(day); value != nil {
			sum += *value
			samples++
		}
	}
	if samples == 0 {
		return nil, 0
	}
	return new(sum / float64(samples)), samples
}

func optionalMassKG(grams *float64) *float64 {
	if grams == nil {
		return nil
	}
	return new(*grams / 1000)
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
