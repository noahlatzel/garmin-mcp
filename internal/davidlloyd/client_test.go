package davidlloyd

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubDoer returns canned responses keyed by URL path, recording the last request.
type stubDoer struct {
	responses map[string]*http.Response
	lastReq   *http.Request
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.lastReq = req
	if resp, ok := s.responses[req.URL.Path]; ok {
		return resp, nil
	}
	return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("no stub"))}, nil
}

func resp(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: header}
}

func TestLoginParsesUUIDAndCookie(t *testing.T) {
	h := http.Header{}
	h.Set("Set-Cookie", "JSESSIONID=abc123; Path=/; HttpOnly")
	doer := &stubDoer{responses: map[string]*http.Response{
		"/np/exerciser/login": resp(http.StatusOK, `{"uuid":"user-1","name":"x"}`, h),
	}}
	c := NewClientWithDoer(doer)

	sess, err := c.Login(context.Background(), "davidlloydclubs", "u@x.com", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if sess.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", sess.UserID)
	}
	if sess.Cookie != "JSESSIONID=abc123" {
		t.Errorf("Cookie = %q, want JSESSIONID=abc123 (attributes stripped)", sess.Cookie)
	}
	// Netpulse headers must be present.
	if got := doer.lastReq.Header.Get("X-NP-API-Version"); got != "1.5" {
		t.Errorf("X-NP-API-Version = %q, want 1.5", got)
	}
}

func TestLoginErrorsOnBadStatus(t *testing.T) {
	doer := &stubDoer{responses: map[string]*http.Response{
		"/np/exerciser/login": resp(http.StatusUnauthorized, `bad creds`, nil),
	}}
	c := NewClientWithDoer(doer)
	if _, err := c.Login(context.Background(), "davidlloydclubs", "u", "p"); err == nil {
		t.Fatal("expected error on HTTP 401, got nil")
	}
}

func TestLatestBodyParsesMetrics(t *testing.T) {
	body := `[
		{"type":"WEIGHT_KG","value":82.1,"createdAt":"2026-06-20T06:10:23Z","source":"fithub","sourceLabel":"Tanita"},
		{"type":"BMI","value":23.7,"createdAt":"2026-06-20T06:10:23Z","source":"fithub","sourceLabel":"Tanita"}
	]`
	doer := &stubDoer{responses: map[string]*http.Response{
		"/measurements/api/v1.0/exercisers/user-1/body/latest": resp(http.StatusOK, body, nil),
	}}
	c := NewClientWithDoer(doer)

	metrics, err := c.LatestBody(context.Background(), &Session{UserID: "user-1", Cookie: "JSESSIONID=abc"})
	if err != nil {
		t.Fatalf("LatestBody: %v", err)
	}
	if len(metrics) != 2 {
		t.Fatalf("got %d metrics, want 2", len(metrics))
	}
	if metrics[0].Type != WeightType || metrics[0].Value != 82.1 {
		t.Errorf("first metric = %+v, want WEIGHT_KG 82.1", metrics[0])
	}
	if got := doer.lastReq.Header.Get("Cookie"); got != "JSESSIONID=abc" {
		t.Errorf("Cookie header = %q, want JSESSIONID=abc", got)
	}
}

func TestLatestBodyUnauthorizedSignalsRelogin(t *testing.T) {
	doer := &stubDoer{responses: map[string]*http.Response{
		"/measurements/api/v1.0/exercisers/user-1/body/latest": resp(http.StatusUnauthorized, ``, nil),
	}}
	c := NewClientWithDoer(doer)
	_, err := c.LatestBody(context.Background(), &Session{UserID: "user-1", Cookie: "x"})
	if err != errUnauthorized {
		t.Fatalf("err = %v, want errUnauthorized", err)
	}
}

func TestServiceBootstrapThenLatestWeight(t *testing.T) {
	h := http.Header{}
	h.Set("Set-Cookie", "JSESSIONID=s1")
	doer := &stubDoer{responses: map[string]*http.Response{
		"/np/exerciser/login": resp(http.StatusOK, `{"uuid":"u1"}`, h),
		"/measurements/api/v1.0/exercisers/u1/body/latest": resp(http.StatusOK,
			`[{"type":"BMI","value":23.7},{"type":"WEIGHT_KG","value":82.1,"createdAt":"2026-06-20T06:10:23Z","sourceLabel":"Tanita"}]`, nil),
	}}
	sessionPath := t.TempDir() + "/session.json"
	svc := NewServiceWithClient(NewClientWithDoer(doer), sessionPath)

	// Before bootstrap, reads fail with ErrNotLinked.
	if _, err := svc.LatestWeight(context.Background()); err == nil {
		t.Fatal("expected ErrNotLinked before bootstrap")
	}

	// Bootstrap stores only the session token (password not retained).
	if err := svc.Bootstrap(context.Background(), "", "u@x.com", "pw"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	w, err := svc.LatestWeight(context.Background())
	if err != nil {
		t.Fatalf("LatestWeight: %v", err)
	}
	if w.Kg != 82.1 || w.MeasuredAt != "2026-06-20T06:10:23Z" || w.Source != "Tanita" {
		t.Errorf("weight = %+v, want 82.1 / 2026-06-20T06:10:23Z / Tanita", w)
	}
}
