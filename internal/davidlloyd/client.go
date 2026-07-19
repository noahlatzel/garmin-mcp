package davidlloyd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// egymAPIHost serves the member's body measurements once authenticated.
	egymAPIHost = "https://mobile-api.int.api.egym.com"

	// netpulseUA and the X-NP-* headers identify the mobile client; the backend
	// rejects requests without them.
	netpulseUA = "okhttp/3.12.3"

	// WeightType is the body-metric type carrying body weight in kilograms.
	WeightType = "WEIGHT_KG"
)

// HTTPDoer is the subset of http.Client the Client needs, so tests can stub
// transport without a live network.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client talks to the Netpulse front door and the eGym measurements API. It holds
// no credentials and no session state; the Service layer owns those.
type Client struct {
	doer HTTPDoer
}

// NewClient builds a Client with a sensible default HTTP client (timeout, no
// shared package state).
func NewClient() *Client {
	return &Client{doer: &http.Client{Timeout: 30 * time.Second}}
}

// NewClientWithDoer builds a Client over a custom doer (used in tests).
func NewClientWithDoer(doer HTTPDoer) *Client {
	return &Client{doer: doer}
}

// Session is an authenticated David Lloyd session: the exerciser id and the
// cookie to replay on measurement requests.
type Session struct {
	UserID string
	Cookie string
}

// BodyMetric is one measurement returned by body/latest. Value units are implied
// by Type (e.g. WEIGHT_KG is kilograms). CreatedAt is the measurement time.
type BodyMetric struct {
	Type                string  `json:"type"`
	Value               float64 `json:"value"`
	CreatedAt           string  `json:"createdAt"`
	Source              string  `json:"source"`
	SourceLabel         string  `json:"sourceLabel"`
	ValueInterpretation string  `json:"valueInterpretation"`
}

// Login authenticates against the Netpulse front door for the given brand and
// returns the session (exerciser id + cookie). Credentials are sent once, form-
// encoded over TLS; they are not retained by the Client.
func (c *Client) Login(ctx context.Context, brand, username, password string) (*Session, error) {
	form := url.Values{"username": {username}, "password": {password}, "relogin": {"false"}}
	endpoint := fmt.Sprintf("https://%s.netpulse.com/np/exerciser/login", url.PathEscape(brand))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	npHeaders(req)

	body, status, setCookie, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("david lloyd login failed (HTTP %d): %s", status, snippet(body))
	}
	var out struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding login response: %w", err)
	}
	if out.UUID == "" {
		return nil, fmt.Errorf("login response had no exerciser uuid")
	}

	cookie := setCookie
	if i := strings.Index(cookie, ";"); i >= 0 {
		cookie = cookie[:i] // keep just "JSESSIONID=..."
	}
	if cookie == "" {
		return nil, fmt.Errorf("login response set no session cookie")
	}
	return &Session{UserID: out.UUID, Cookie: cookie}, nil
}

// LatestBody returns the latest value of every body metric for the session.
func (c *Client) LatestBody(ctx context.Context, sess *Session) ([]BodyMetric, error) {
	endpoint := fmt.Sprintf("%s/measurements/api/v1.0/exercisers/%s/body/latest",
		egymAPIHost, url.PathEscape(sess.UserID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", sess.Cookie)
	req.Header.Set("Accept", "application/json")
	npHeaders(req)

	body, status, _, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, errUnauthorized
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("david lloyd body/latest failed (HTTP %d): %s", status, snippet(body))
	}
	var metrics []BodyMetric
	if err := json.Unmarshal(body, &metrics); err != nil {
		return nil, fmt.Errorf("decoding body metrics: %w", err)
	}
	return metrics, nil
}

// do executes the request and returns body, status and the first Set-Cookie
// header. It always drains and closes the body.
func (c *Client) do(req *http.Request) (body []byte, status int, setCookie string, err error) {
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("david lloyd %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, "", fmt.Errorf("reading response: %w", err)
	}
	return raw, resp.StatusCode, resp.Header.Get("Set-Cookie"), nil
}

// npHeaders sets the Netpulse client headers the backend expects.
func npHeaders(req *http.Request) {
	req.Header.Set("User-Agent", netpulseUA)
	req.Header.Set("X-NP-API-Version", "1.5")
	req.Header.Set("X-NP-App-Version", "9999")
	req.Header.Set("X-NP-User-Agent",
		"clientType=MOBILE_DEVICE; devicePlatform=ANDROID; "+
			"appName=com.netpulse.mobile.davidlloydclubstest; appVersion=9999")
}

func snippet(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
