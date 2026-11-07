package garmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestActivityTools(t *testing.T) {
	const search = "/activitylist-service/activities/search/activities"
	const detail = "/activity-service/activity/123"
	doer := &recordDoer{t: t, routes: map[string]string{
		search: `[{"activityId":123,"activityType":{"typeKey":"running"},"distance":5000,"startLatitude":48}]`,
	}}
	svc := testService(t, doer)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	server, err := NewMCPServer(svc).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_activities", "get_activity_details"} {
		found := false
		for _, tool := range tools.Tools {
			if tool.Name == name {
				found = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
			}
		}
		if !found {
			t.Fatalf("%s is missing or lacks the read-only hint", name)
		}
	}

	result, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "list_activities", Arguments: map[string]any{
			"activity_type": "running", "start_date": "2026-11-01", "limit": "5",
		},
	})
	if err != nil || result.IsError {
		t.Fatalf("list_activities: %+v, %v", result, err)
	}
	body := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(body, `"distance":5000`) || strings.Contains(body, "startLatitude") {
		t.Fatalf("unexpected activity projection: %s", body)
	}
	req := doer.last()
	query, err := url.ParseQuery(req.query)
	if err != nil || req.method != http.MethodGet || req.path != search ||
		query.Get("activityType") != "running" || query.Get("startDate") != "2026-11-01" || query.Get("limit") != "5" {
		t.Fatalf("unexpected list request: %+v", req)
	}

	doer.routes = map[string]string{detail: `{"activityId":123,"summaryDTO":{"averageHR":141,"startLongitude":11}}`}
	result, err = client.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_activity_details", Arguments: map[string]any{"activity_id": "123"},
	})
	if err != nil || result.IsError {
		t.Fatalf("get_activity_details: %+v, %v", result, err)
	}
	body = result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(body, `"averageHR":141`) || strings.Contains(body, "startLongitude") ||
		strings.Contains(body, "laps") || doer.last().method != http.MethodGet || doer.last().path != detail {
		t.Fatalf("unexpected detail response or request: %s %+v", body, doer.last())
	}
}

func TestActivityLapsAndInvalidInput(t *testing.T) {
	doer := &recordDoer{t: t, routes: map[string]string{"/laps": `{"lapDTOs":[{"lapIndex":1,"duration":300,"startLatitude":48}]}`}}
	svc := testService(t, doer)
	details, err := svc.activityDetails(context.Background(), activityDetailInput{ActivityID: 123, IncludeLaps: true})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(details.Laps)
	if err != nil || string(body) != `[{"lapIndex":1,"duration":300}]` || doer.last().method != http.MethodGet {
		t.Fatalf("unexpected lap response: %s, %v", body, err)
	}

	doer.failOnAny = true
	if _, err := svc.listActivities(context.Background(), activityListInput{StartDate: "2026-02-30"}); err == nil {
		t.Fatal("accepted an invalid date")
	}
	if _, err := svc.activityDetails(context.Background(), activityDetailInput{ActivityID: -1}); err == nil {
		t.Fatal("accepted a negative ID")
	}
}
