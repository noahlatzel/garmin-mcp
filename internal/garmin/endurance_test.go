package garmin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestEnduranceWorkoutTools(t *testing.T) {
	for _, tc := range []struct {
		tool, sport, target, targetJSON string
	}{
		{"create_run_workout", "running", "pace", "pace.zone"},
		{"create_cycling_workout", "cycling", "power", "power.zone"},
	} {
		t.Run(tc.sport, func(t *testing.T) {
			doer := &recordDoer{t: t, failOnAny: true, routes: map[string]string{
				"/workout-service/workout":      `{"workoutId":123,"sportType":{"sportTypeKey":"` + tc.sport + `"}}`,
				"/workout-service/schedule/123": `{"workoutScheduleId":456}`,
			}}
			ctx := context.Background()
			st, ct := mcp.NewInMemoryTransports()
			server, err := NewMCPServer(testService(t, doer)).Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()

			args := map[string]any{
				"name": "Intervals", "schedule_date": "2030-01-02", "dry_run": true,
				"steps": `[{"distance_m":"500","repeats":2,"recovery_secs":60,"target_kind":"` + tc.target + `","target_low":3,"target_high":4}]`,
			}
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: tc.tool, Arguments: args})
			if err != nil || result.IsError {
				t.Fatalf("preview: %+v, %v", result, err)
			}
			body := result.Content[0].(*mcp.TextContent).Text
			for _, want := range []string{tc.sport, tc.targetJSON, `"stepTypeKey":"recovery"`, `"stepOrder":3`, `"endConditionValue":500`} {
				if !strings.Contains(body, want) {
					t.Fatalf("missing %q in preview: %s", want, body)
				}
			}
			args["dry_run"] = false
			doer.failOnAny = false
			result, err = client.CallTool(ctx, &mcp.CallToolParams{Name: tc.tool, Arguments: args})
			if err != nil || result.IsError {
				t.Fatalf("create: %+v, %v", result, err)
			}
			if len(doer.reqs) != 2 || doer.reqs[0].method != http.MethodPost ||
				doer.reqs[0].path != "/workout-service/workout" ||
				doer.reqs[1].method != http.MethodPost || doer.reqs[1].path != "/workout-service/schedule/123" {
				t.Fatalf("unexpected Garmin requests: %+v", doer.reqs)
			}
		})
	}
}
