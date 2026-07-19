package garmin

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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
	o1, o2, err := c.Login(context.Background(), Credentials{Username: "u@example.com", Password: "pw"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if o1.Token != "OT1" || o1.TokenSecret != "OTS1" {
		t.Fatalf("bad OAuth1 token: %+v", o1)
	}
	if o2.AccessToken != "AT" || o2.RefreshToken != "RT" || o2.ExpiresIn != 3600 {
		t.Fatalf("bad OAuth2 token: %+v", o2)
	}
	if o2.ExpiresAt.IsZero() || o2.Expired() {
		t.Fatalf("ExpiresAt not set / already expired: %+v", o2)
	}
	if !strings.Contains(m.lastLogin, "_csrf=CSRF-TOKEN-123") || !strings.Contains(m.lastLogin, "username=u%40example.com") {
		t.Fatalf("login form did not carry csrf/username: %q", m.lastLogin)
	}
}

func TestLogin_MFA(t *testing.T) {
	m := &mockDoer{t: t, mfa: true}
	c := NewClientWithDoer(m)
	called := false
	_, o2, err := c.Login(context.Background(), Credentials{
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
	if o2.AccessToken != "AT" {
		t.Fatalf("bad token after MFA: %+v", o2)
	}
}

func TestLogin_MFARequiredButNoCallback(t *testing.T) {
	m := &mockDoer{t: t, mfa: true}
	c := NewClientWithDoer(m)
	if _, _, err := c.Login(context.Background(), Credentials{Username: "u", Password: "pw"}); err == nil {
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
