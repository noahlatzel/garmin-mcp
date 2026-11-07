package garmin

import (
	"math"
	"testing"
)

func TestLactateThresholdSpeedAndPace(t *testing.T) {
	for _, raw := range []float64{0.35277679, 3.5277679} {
		speed, pace := lactateThresholdSpeedAndPace(&raw)
		if speed == nil || pace == nil || math.Abs(*speed-3.5277679) > 1e-9 || math.Round(*pace) != 283 {
			t.Fatalf("unexpected conversion for %g", raw)
		}
	}
	for _, raw := range []*float64{nil, new(0.0), new(-1.0)} {
		if speed, pace := lactateThresholdSpeedAndPace(raw); speed != nil || pace != nil {
			t.Fatal("expected missing speed and pace")
		}
	}
}
