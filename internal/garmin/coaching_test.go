package garmin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCoachingHealthSummary(t *testing.T) {
	doer := &recordDoer{t: t, routes: map[string]string{
		"/v2/dailysummaries":         `{"data":[{"calendarDate":"2026-11-02","heartRate":{"restingValue":48},"calories":{"burnedTotal":2500,"consumedValue":2400}}]}`,
		"/hrv/daily/":                `{"hrvSummaries":[{"calendarDate":"2026-11-02","lastNightAvg":65}]}`,
		"/trainingreadiness/":        `[]`,
		"/daterangesnapshot":         `{"dateWeightList":[{"calendarDate":"2026-11-02","timestampGMT":"2026-11-02T08:00:00","weight":72000,"bmi":23,"bodyFat":18.5,"bodyWater":55,"muscleMass":34000,"boneMass":3200},{"calendarDate":"2026-11-02","timestampGMT":"2026-11-02T07:00:00","weight":73000,"bodyFat":99}]}`,
		"/dailySleepSummariesByDate": `[{"calendarDate":"2026-11-02","sleepTimeSeconds":27000,"sleepScores":{"overall":{"value":90}}}]`,
	}}
	got, err := testService(t, doer).healthSummary(context.Background(), coachingRange{"2026-11-02", "2026-11-03"})
	if err != nil || len(got.Issues) != 0 || len(got.Days) != 2 {
		t.Fatalf("health summary: %+v, %v", got, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, want := range []string{`"resting_heart_rate_bpm":48`, `"weight_kg":72`, `"weight_7_day_mean_kg":72`, `"bmi":23`, `"body_fat_percent":18.5`, `"body_water_percent":55`, `"skeletal_muscle_mass_kg":34`, `"bone_mass_kg":3.2`, `"sleep_seconds":27000`, `"hrv_last_night_ms":65`, `"total_expenditure_kcal":2500`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "2400") || got.Days[1].BodyFatPercent != nil || got.Days[1].WeightKG != nil || got.Days[1].Weight7DaySamples != 1 {
		t.Fatalf("unexpected intake or invented weigh-in in %s", body)
	}
}

func TestStrengthSetsUseRecordedExternalWeight(t *testing.T) {
	doer := &recordDoer{t: t, routes: map[string]string{
		"/exerciseSets": `{"exerciseSets":[{"setType":"ACTIVE","weight":20000,"repetitionCount":5,"exercises":[{"name":"SQUAT"}]},{"setType":"REST","weight":-1},{"setType":"ACTIVE","duration":30}]}`,
	}}
	sets, err := testService(t, doer).readStrengthSets(context.Background(), "token", 123)
	if err != nil || len(sets) != 2 {
		t.Fatalf("sets: %+v, %v", sets, err)
	}
	var totals strengthTotals
	for _, set := range sets {
		totals.add(set)
	}
	if totals.VolumeSets != 1 || totals.Volume == nil || *totals.Volume != 100 || sets[1].Reps != nil {
		t.Fatalf("unexpected totals or timed set: %+v, %+v", totals, sets[1])
	}
}
