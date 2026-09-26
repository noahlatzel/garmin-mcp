package garmin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// mockDoer routes requests by method+path to canned responses and records the
// last login POST form so tests can assert what was sent.
type mockDoer struct {
	t          *testing.T
	mfa        bool // when true, the first signin POST returns an MFA page
	mfaServed  bool
	lastLogin  string
	lastMFA    string
	preauthHit int

	diReject    func(clientID string) bool // DI client ids the token service refuses
	diHits      []string                   // client ids presented, in order
	lastDIForm  url.Values
	diRefreshes int
	onRefresh   func() // runs inside each DI refresh request
}

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func (m *mockDoer) Do(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	switch {
	case strings.HasSuffix(path, "/sso/embed"):
		return resp(200, `<html><title>GARMIN Authentication Application</title></html>`), nil

	case strings.HasSuffix(path, "/sso/signin") && req.Method == http.MethodGet:
		return resp(200, `<form><input name="_csrf" value="CSRF-TOKEN-123"/></form>`), nil

	case strings.HasSuffix(path, "/sso/signin") && req.Method == http.MethodPost:
		body, _ := io.ReadAll(req.Body)
		m.lastLogin = string(body)
		if m.mfa && !m.mfaServed {
			return resp(200, `<html><title>Enter MFA code for login</title>`+
				`<input name="_csrf" value="MFA-CSRF-456"/></html>`), nil
		}
		return resp(200, `<html><body><a href="https://sso.garmin.com/sso/embed?ticket=ST-TICKET-789">go</a></body></html>`), nil

	case strings.HasSuffix(path, "/verifyMFA/loginEnterMfaCode"):
		body, _ := io.ReadAll(req.Body)
		m.lastMFA = string(body)
		m.mfaServed = true
		return resp(200, `<html><body><a href="https://sso.garmin.com/sso/embed?ticket=ST-TICKET-789">go</a></body></html>`), nil

	case strings.HasSuffix(path, "/di-oauth2-service/oauth/token"):
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		m.lastDIForm = form
		id, secret, ok := req.BasicAuth()
		if !ok || secret != "" || id != form.Get("client_id") {
			m.t.Errorf("DI request Basic auth = %q/%q/%v, want client_id %q with empty secret", id, secret, ok, form.Get("client_id"))
		}
		m.diHits = append(m.diHits, id)
		if m.diReject != nil && m.diReject(id) {
			return resp(400, `{"error":"invalid_client"}`), nil
		}
		if form.Get("grant_type") == "refresh_token" {
			m.diRefreshes++
			if m.onRefresh != nil {
				m.onRefresh()
			}
			return resp(200, `{"access_token":"DAT2","refresh_token":"DRT2","expires_in":3600}`), nil
		}
		return resp(200, `{"access_token":"DAT","refresh_token":"DRT","expires_in":3600}`), nil

	case strings.HasSuffix(path, "oauth_consumer.json"):
		return resp(200, `{"consumer_key":"CK","consumer_secret":"CS"}`), nil

	case strings.Contains(path, "/oauth/preauthorized"):
		m.preauthHit++
		if req.Header.Get("Authorization") == "" || !strings.HasPrefix(req.Header.Get("Authorization"), "OAuth ") {
			m.t.Errorf("preauthorized missing OAuth1 Authorization header: %q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("User-Agent") != androidUA {
			m.t.Errorf("preauthorized wrong UA: %q", req.Header.Get("User-Agent"))
		}
		return resp(200, "oauth_token=OT1&oauth_token_secret=OTS1"), nil

	case strings.Contains(path, "/oauth/exchange/user/2.0"):
		if !strings.Contains(req.Header.Get("Authorization"), `oauth_token=`) {
			m.t.Errorf("exchange Authorization missing oauth_token: %q", req.Header.Get("Authorization"))
		}
		return resp(200, `{"access_token":"AT","token_type":"Bearer","refresh_token":"RT","expires_in":3600}`), nil
	}
	m.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func TestLogin_HappyPath(t *testing.T) {
	m := &mockDoer{t: t}
	c := NewClientWithDoer(m)
	tokens, err := c.Login(context.Background(), Credentials{Username: "u@example.com", Password: "pw"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	di := tokens.DI
	if di == nil || di.AccessToken != "DAT" || di.RefreshToken != "DRT" || di.ClientID != diClientIDs[0] {
		t.Fatalf("bad DI token: %+v", di)
	}
	if di.Expired() {
		t.Fatalf("DI token already expired: %+v", di)
	}
	if tokens.OAuth1 != nil || m.preauthHit != 0 {
		t.Fatalf("DI login should not touch OAuth1 (oauth1 %+v, preauthorized hit %d times)", tokens.OAuth1, m.preauthHit)
	}
	want := url.Values{
		"client_id":      {diClientIDs[0]},
		"service_ticket": {"ST-TICKET-789"},
		"grant_type":     {diTicketGrant},
		"service_url":    {"https://sso.garmin.com/sso/embed"},
	}
	if m.lastDIForm.Encode() != want.Encode() {
		t.Fatalf("DI exchange form = %q, want %q", m.lastDIForm.Encode(), want.Encode())
	}
	if !strings.Contains(m.lastLogin, "_csrf=CSRF-TOKEN-123") || !strings.Contains(m.lastLogin, "username=u%40example.com") {
		t.Fatalf("login form did not carry csrf/username: %q", m.lastLogin)
	}
}

func TestLogin_DITriesNextClientID(t *testing.T) {
	m := &mockDoer{t: t, diReject: func(id string) bool { return id == diClientIDs[0] }}
	tokens, err := NewClientWithDoer(m).Login(context.Background(), Credentials{Username: "u", Password: "pw"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tokens.DI == nil || tokens.DI.ClientID != diClientIDs[1] {
		t.Fatalf("DI token = %+v, want one issued to %s", tokens.DI, diClientIDs[1])
	}
}

func TestLogin_FallsBackToOAuth1WhenDIRefuses(t *testing.T) {
	m := &mockDoer{t: t, diReject: func(string) bool { return true }}
	tokens, err := NewClientWithDoer(m).Login(context.Background(), Credentials{Username: "u", Password: "pw"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !slices.Equal(m.diHits, diClientIDs) {
		t.Fatalf("DI client ids tried = %v, want %v", m.diHits, diClientIDs)
	}
	if tokens.DI != nil {
		t.Fatalf("unexpected DI token: %+v", tokens.DI)
	}
	o1, o2 := tokens.OAuth1, tokens.OAuth2
	if o1 == nil || o1.Token != "OT1" || o1.TokenSecret != "OTS1" {
		t.Fatalf("bad OAuth1 token: %+v", o1)
	}
	if o2 == nil || o2.AccessToken != "AT" || o2.RefreshToken != "RT" || o2.ExpiresIn != 3600 || o2.Expired() {
		t.Fatalf("bad OAuth2 token: %+v", o2)
	}
}

func TestLogin_MFA(t *testing.T) {
	m := &mockDoer{t: t, mfa: true}
	c := NewClientWithDoer(m)
	called := false
	tokens, err := c.Login(context.Background(), Credentials{
		Username: "u", Password: "pw",
		MFA: func(context.Context) (string, error) { called = true; return "654321", nil },
	})
	if err != nil {
		t.Fatalf("Login with MFA: %v", err)
	}
	if !called {
		t.Fatal("MFA callback was not invoked")
	}
	if !strings.Contains(m.lastMFA, "mfa-code=654321") || !strings.Contains(m.lastMFA, "_csrf=MFA-CSRF-456") {
		t.Fatalf("MFA POST missing code/csrf: %q", m.lastMFA)
	}
	if tokens.DI == nil || tokens.DI.AccessToken != "DAT" {
		t.Fatalf("bad token after MFA: %+v", tokens.DI)
	}
}

func TestLogin_MFARequiredButNoCallback(t *testing.T) {
	m := &mockDoer{t: t, mfa: true}
	c := NewClientWithDoer(m)
	if _, err := c.Login(context.Background(), Credentials{Username: "u", Password: "pw"}); err == nil {
		t.Fatal("expected error when MFA required but no callback supplied")
	}
}

func TestRefresh(t *testing.T) {
	m := &mockDoer{t: t}
	c := NewClientWithDoer(m)
	o2, err := c.Refresh(context.Background(), &OAuth1Token{Token: "OT1", TokenSecret: "OTS1"})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if o2.AccessToken != "AT" {
		t.Fatalf("bad refreshed token: %+v", o2)
	}
	if m.preauthHit != 0 {
		t.Fatalf("Refresh should not hit preauthorized (it did %d times)", m.preauthHit)
	}
}

func TestRefreshDI(t *testing.T) {
	m := &mockDoer{t: t}
	got, err := NewClientWithDoer(m).RefreshDI(context.Background(), &DIToken{RefreshToken: "DRT", ClientID: "CID"})
	if err != nil {
		t.Fatalf("RefreshDI: %v", err)
	}
	if got.AccessToken != "DAT2" || got.RefreshToken != "DRT2" || got.ClientID != "CID" || got.Expired() {
		t.Fatalf("bad refreshed DI token: %+v", got)
	}
	want := url.Values{"grant_type": {"refresh_token"}, "client_id": {"CID"}, "refresh_token": {"DRT"}}
	if m.lastDIForm.Encode() != want.Encode() {
		t.Fatalf("refresh form = %q, want %q", m.lastDIForm.Encode(), want.Encode())
	}
}

func TestRefreshDI_RejectedNeedsRelink(t *testing.T) {
	m := &mockDoer{t: t, diReject: func(string) bool { return true }}
	_, err := NewClientWithDoer(m).RefreshDI(context.Background(), &DIToken{RefreshToken: "DRT", ClientID: "CID"})
	if !errors.Is(err, ErrRelinkRequired) {
		t.Fatalf("RefreshDI error = %v, want ErrRelinkRequired", err)
	}
}

// TestBearer_ConcurrentCallsRefreshOnce runs two tool calls at once on an
// expired token: the second must reuse the first's refresh rather than spend
// the rotated refresh token again.
func TestBearer_ConcurrentCallsRefreshOnce(t *testing.T) {
	ctx := context.Background()
	m := &mockDoer{t: t}
	store := NewTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err := store.Save(&StoredTokens{DI: &DIToken{
		AccessToken: "DAT", RefreshToken: "DRT", ClientID: "CID", ExpiresAt: time.Now().Add(-time.Minute),
	}}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	svc := &Service{client: NewClientWithDoer(m), store: store}

	type result struct {
		token string
		err   error
	}
	second := make(chan result, 1)
	secondRefreshing := make(chan struct{})
	refreshes := 0
	m.onRefresh = func() {
		refreshes++
		if refreshes > 1 {
			close(secondRefreshing)
			return
		}
		go func() {
			tok, err := svc.bearer(ctx)
			second <- result{tok, err}
		}()
		// Hold the first refresh open long enough for an unlocked second
		// call to reach Garmin too.
		select {
		case <-secondRefreshing:
		case <-time.After(100 * time.Millisecond):
		}
	}

	tok, err := svc.bearer(ctx)
	if err != nil || tok != "DAT2" {
		t.Fatalf("first bearer = %q, %v; want DAT2", tok, err)
	}
	if r := <-second; r.err != nil || r.token != "DAT2" {
		t.Fatalf("second bearer = %q, %v; want DAT2", r.token, r.err)
	}
	if m.diRefreshes != 1 {
		t.Fatalf("refreshed %d times, want 1", m.diRefreshes)
	}
	stored, err := store.Load()
	if err != nil || stored.DI.RefreshToken != "DRT2" {
		t.Fatalf("stored DI token = %+v, %v; want the rotated refresh token", stored.DI, err)
	}
}

func TestOAuthEscape(t *testing.T) {
	// RFC 5849 examples.
	cases := map[string]string{
		"abcABC123": "abcABC123",
		"-._~":      "-._~",
		" ":         "%20",
		"+":         "%2B",
		"=":         "%3D",
		"&":         "%26",
	}
	for in, want := range cases {
		if got := oauthEscape(in); got != want {
			t.Errorf("oauthEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSignDeterministic(t *testing.T) {
	params := map[string]string{
		"oauth_consumer_key":     "CK",
		"oauth_nonce":            "n",
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        "1",
		"oauth_version":          "1.0",
	}
	a := sign("GET", "https://connectapi.garmin.com/oauth-service/oauth/preauthorized", params, "CS", "")
	b := sign("GET", "https://connectapi.garmin.com/oauth-service/oauth/preauthorized", params, "CS", "")
	if a == "" || a != b {
		t.Fatalf("signature not deterministic/non-empty: %q vs %q", a, b)
	}
}
