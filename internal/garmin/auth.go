package garmin

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Garmin SSO + OAuth endpoints. Login uses the no-clientId web-widget SSO path,
// which a stock net/http client reaches without TLS-fingerprint impersonation.
// The clientId mobile/portal path is Cloudflare rate-limited (429), so we avoid it.
const (
	ssoBase   = "https://sso.garmin.com"
	ssoOrigin = "https://sso.garmin.com/sso"
	embedHost = "https://sso.garmin.com/sso/embed"

	embedURL  = ssoOrigin + "/embed?id=gauth-widget&embedWidget=true&gauthHost=" + embedHost
	signinURL = ssoOrigin + "/signin?" + signinParams
	// The MFA POST carries the same widget query params as signin, plus a
	// fromPage field; without them Garmin answers "try again." and re-renders.
	mfaURL      = ssoOrigin + "/verifyMFA/loginEnterMfaCode?" + signinParams
	mfaFromPage = "setupEnterMfaCode"

	preauthURL     = "https://connectapi.garmin.com/oauth-service/oauth/preauthorized"
	exchangeURL    = "https://connectapi.garmin.com/oauth-service/oauth/exchange/user/2.0"
	connectAPIHost = "https://connectapi.garmin.com"

	// Garmin Connect Mobile (Android) OAuth1 consumer credentials. These are the
	// Garmin app's own credentials, baked into the APK — public and not a secret
	// of ours or any user data. Vendored so login doesn't depend on an external
	// source. If Garmin ever rotates them, set GARMIN_OAUTH_CONSUMER_URL to a JSON
	// source ({"consumer_key":..,"consumer_secret":..}) to override without a rebuild.
	defaultConsumerKey    = "fc3e99d2-118c-44b8-8ae3-03370dde24c0"
	defaultConsumerSecret = "E08WAR897WEy2knn7aFBrvegVAf0AFdWBBF"

	// browserUA mimics a desktop Chrome for the SSO widget steps; androidUA is
	// required by the connectapi OAuth exchange and subsequent API calls.
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	androidUA = "com.garmin.android.apps.connectmobile"
)

// signinParams is the SSO widget parameter set. It carries no clientId, which
// puts it in a different Cloudflare rate-limit bucket than the mobile endpoints.
const signinParams = "id=gauth-widget&embedWidget=true" +
	"&gauthHost=" + embedHost +
	"&service=" + embedHost +
	"&source=" + embedHost +
	"&redirectAfterAccountLoginUrl=" + embedHost +
	"&redirectAfterAccountCreationUrl=" + embedHost

var (
	csrfRe   = regexp.MustCompile(`name="_csrf"\s+value="(.+?)"`)
	ticketRe = regexp.MustCompile(`embed\?ticket=([^"]+)"`)
	titleRe  = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
)

// HTTPDoer is the seam through which all Garmin HTTP traffic flows. The default
// is the standard library client; a TLS-fingerprinted implementation can be
// substituted if Garmin later tightens its Cloudflare TLS check, without
// touching the auth flow itself.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// MFAFunc supplies the TOTP code when Garmin demands MFA. It is invoked only if
// the login response asks for it, so the operator can enter the code. Returning
// an error aborts the login.
type MFAFunc func(ctx context.Context) (string, error)

// Credentials are the user's Garmin Connect login. They are never persisted; the
// only thing kept on disk is the resulting OAuth token (see TokenStore).
type Credentials struct {
	Username string
	Password string
	// MFA is called if Garmin requires a TOTP code. May be nil if the account
	// has no MFA; a non-nil value is required to complete an MFA-gated login.
	MFA MFAFunc
}

// OAuth1Token is the long-lived (≈1 year) Garmin OAuth1 token used to mint fresh
// OAuth2 access tokens without re-running the credentialed SSO login.
type OAuth1Token struct {
	Token       string `json:"oauth_token"`
	TokenSecret string `json:"oauth_token_secret"`
}

// OAuth2Token is the short-lived bearer used for connectapi requests.
type OAuth2Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	// ExpiresAt is computed locally at receipt so refresh can be scheduled.
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the access token is at or past its expiry, with a
// 60-second safety margin.
func (t *OAuth2Token) Expired() bool {
	return t == nil || time.Now().Add(60*time.Second).After(t.ExpiresAt)
}

// consumer holds the public Garmin Android OAuth consumer key/secret. It
// defaults to the vendored values above and is only fetched at runtime when
// GARMIN_OAUTH_CONSUMER_URL is set.
type consumer struct {
	Key    string `json:"consumer_key"`
	Secret string `json:"consumer_secret"`
}

// Client performs the Garmin SSO login and OAuth token lifecycle. It is safe for
// sequential use; callers should serialize login/refresh.
type Client struct {
	http     HTTPDoer
	consumer consumer
	// debugDir, when non-empty, receives the raw SSO HTML pages (0600) for
	// diagnosing login failures. Set via GARMIN_DEBUG. The dumps may contain the
	// account email and CSRF/ticket values, so they are written owner-only and
	// should be deleted after diagnosis.
	debugDir string
}

// NewClient builds a Client with a cookie-jar-backed standard library HTTP
// client. Pass a custom HTTPDoer (e.g. utls) only if the default is blocked.
func NewClient() (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("creating cookie jar: %w", err)
	}
	c := &Client{
		http: &http.Client{Timeout: 30 * time.Second, Jar: jar},
	}
	// Debug dumping is opt-in and requires an EXPLICIT directory path — never a
	// shared default like os.TempDir(), since the dumps contain the account email
	// and CSRF/ticket values. The directory must be owned by the caller and not
	// world-writable (checked at write time).
	c.debugDir = strings.TrimSpace(os.Getenv("GARMIN_DEBUG"))
	return c, nil
}

// dump writes a captured page to the debug directory. It refuses a world-writable
// directory and uses O_CREATE|O_EXCL so a pre-existing file or planted symlink at
// the predictable path causes the write to fail rather than clobber/leak.
func (c *Client) dump(name, body string) {
	if c.debugDir == "" {
		return
	}
	info, err := os.Stat(c.debugDir)
	if err != nil || !info.IsDir() {
		slog.Warn("garmin: GARMIN_DEBUG is not a directory; skipping dump", "dir", c.debugDir)
		return
	}
	if info.Mode().Perm()&0o002 != 0 {
		slog.Warn("garmin: refusing to dump into world-writable directory", "dir", c.debugDir)
		return
	}
	path := filepath.Join(c.debugDir, "garmin-debug-"+name+".html")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		slog.Warn("garmin: could not create debug file (already exists?)", "path", path, "err", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(body); err == nil {
		slog.Info("garmin: debug page written", "path", path, "bytes", len(body))
	}
}

// pageTitle returns the <title> text of an HTML page, for error context.
func pageTitle(html string) string {
	if m := titleRe.FindStringSubmatch(html); m != nil {
		return strings.TrimSpace(m[1])
	}
	return "(no <title>)"
}

// NewClientWithDoer builds a Client over a caller-supplied HTTPDoer. The doer
// must maintain its own cookie state across requests (the SSO flow is
// cookie-dependent).
func NewClientWithDoer(doer HTTPDoer) *Client {
	return &Client{http: doer}
}

// Login runs the full web-widget SSO flow and OAuth exchange, returning both the
// long-lived OAuth1 token (for later refresh) and a fresh OAuth2 access token.
// It performs exactly one credentialed POST; callers must avoid retry storms
// (fresh-login misfires can trigger ~48h account blocks).
func (c *Client) Login(ctx context.Context, creds Credentials) (*OAuth1Token, *OAuth2Token, error) {
	if strings.TrimSpace(creds.Username) == "" || creds.Password == "" {
		return nil, nil, fmt.Errorf("username and password are required")
	}

	// 1. Seed session cookies.
	if _, err := c.get(ctx, embedURL, browserUA, ""); err != nil {
		return nil, nil, fmt.Errorf("seeding SSO cookies: %w", err)
	}

	// 2. Fetch the signin form and extract the CSRF token.
	signinHTML, err := c.get(ctx, signinURL, browserUA, embedURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching signin form: %w", err)
	}
	c.dump("signin", signinHTML)
	csrf, err := extract(csrfRe, signinHTML, "CSRF token")
	if err != nil {
		return nil, nil, fmt.Errorf("%w (signin page title: %q)", err, pageTitle(signinHTML))
	}

	// 3. Submit credentials as a form POST.
	form := url.Values{
		"username": {creds.Username},
		"password": {creds.Password},
		"embed":    {"true"},
		"_csrf":    {csrf},
	}
	loginHTML, err := c.postForm(ctx, signinURL, form, browserUA, signinURL)
	if err != nil {
		return nil, nil, fmt.Errorf("submitting credentials: %w", err)
	}
	c.dump("login", loginHTML)

	// 4. Handle MFA if Garmin demands it.
	if mfaRequested(loginHTML) {
		loginHTML, err = c.submitMFA(ctx, creds, loginHTML)
		if err != nil {
			return nil, nil, err
		}
		c.dump("mfa", loginHTML)
	}

	// 5. Extract the service ticket from the success page.
	ticket, err := extract(ticketRe, loginHTML, "service ticket")
	if err != nil {
		return nil, nil, fmt.Errorf("%w (login likely failed: bad credentials, undetected MFA, "+
			"or an unexpected page; response title: %q, %d bytes — set GARMIN_DEBUG to a private "+
			"directory to capture it)",
			err, pageTitle(loginHTML), len(loginHTML))
	}

	// 6. Exchange the ticket for OAuth1, then OAuth2 tokens.
	if err := c.ensureConsumer(ctx); err != nil {
		return nil, nil, err
	}
	oauth1, err := c.preauthorize(ctx, ticket)
	if err != nil {
		return nil, nil, err
	}
	oauth2, err := c.exchange(ctx, oauth1)
	if err != nil {
		return nil, nil, err
	}
	return oauth1, oauth2, nil
}

// Refresh mints a fresh OAuth2 access token from a stored OAuth1 token, without
// any credentialed login (the preferred path — avoids the 429-prone SSO flow).
func (c *Client) Refresh(ctx context.Context, oauth1 *OAuth1Token) (*OAuth2Token, error) {
	if oauth1 == nil || oauth1.Token == "" {
		return nil, fmt.Errorf("no OAuth1 token to refresh from; a full Login is required")
	}
	if err := c.ensureConsumer(ctx); err != nil {
		return nil, err
	}
	return c.exchange(ctx, oauth1)
}

// submitMFA invokes the MFA callback and posts the resulting code.
func (c *Client) submitMFA(ctx context.Context, creds Credentials, prevHTML string) (string, error) {
	if creds.MFA == nil {
		return "", fmt.Errorf("Garmin requires an MFA code but no MFA callback was provided")
	}
	code, err := creds.MFA(ctx)
	if err != nil {
		return "", fmt.Errorf("obtaining MFA code: %w", err)
	}
	form := url.Values{
		"mfa-code": {strings.TrimSpace(code)},
		"embed":    {"true"},
		"fromPage": {mfaFromPage},
	}
	// The MFA page carries its own CSRF token; reuse it if present.
	if csrf, e := extract(csrfRe, prevHTML, "CSRF token"); e == nil {
		form.Set("_csrf", csrf)
	}
	html, err := c.postForm(ctx, mfaURL, form, browserUA, signinURL)
	if err != nil {
		return "", fmt.Errorf("submitting MFA code: %w", err)
	}
	return html, nil
}

// ensureConsumer populates the Garmin OAuth consumer credentials. By default it
// uses the vendored Android app credentials; GARMIN_OAUTH_CONSUMER_URL overrides
// with a fetched JSON source for the rare case Garmin rotates them.
func (c *Client) ensureConsumer(ctx context.Context) error {
	if c.consumer.Key != "" {
		return nil
	}
	if override := strings.TrimSpace(os.Getenv("GARMIN_OAUTH_CONSUMER_URL")); override != "" {
		body, err := c.get(ctx, override, browserUA, "")
		if err != nil {
			return fmt.Errorf("fetching OAuth consumer credentials from %s: %w", override, err)
		}
		if err := json.Unmarshal([]byte(body), &c.consumer); err != nil {
			return fmt.Errorf("parsing OAuth consumer credentials from %s: %w", override, err)
		}
		if c.consumer.Key == "" || c.consumer.Secret == "" {
			return fmt.Errorf("override OAuth consumer credentials at %s were empty", override)
		}
		return nil
	}
	c.consumer = consumer{Key: defaultConsumerKey, Secret: defaultConsumerSecret}
	return nil
}

// preauthorize trades the SSO service ticket for an OAuth1 token (the request is
// OAuth1-signed with only the consumer credentials).
func (c *Client) preauthorize(ctx context.Context, ticket string) (*OAuth1Token, error) {
	q := url.Values{
		"ticket":             {ticket},
		"login-url":          {embedHost},
		"accepts-mfa-tokens": {"true"},
	}
	full := preauthURL + "?" + q.Encode()
	header := c.oauth1Header(http.MethodGet, full, nil, "", "")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", header)
	req.Header.Set("User-Agent", androidUA)

	body, _, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("preauthorized request: %w", err)
	}
	vals, err := url.ParseQuery(body)
	if err != nil {
		return nil, fmt.Errorf("parsing OAuth1 response: %w", err)
	}
	tok := &OAuth1Token{Token: vals.Get("oauth_token"), TokenSecret: vals.Get("oauth_token_secret")}
	if tok.Token == "" || tok.TokenSecret == "" {
		return nil, fmt.Errorf("preauthorized returned no OAuth1 token (body: %.120q)", body)
	}
	return tok, nil
}

// exchange trades an OAuth1 token for an OAuth2 access/refresh token. The POST is
// OAuth1-signed with the consumer credentials and the OAuth1 token.
func (c *Client) exchange(ctx context.Context, oauth1 *OAuth1Token) (*OAuth2Token, error) {
	header := c.oauth1Header(http.MethodPost, exchangeURL, nil, oauth1.Token, oauth1.TokenSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeURL, strings.NewReader(""))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", header)
	req.Header.Set("User-Agent", androidUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	body, _, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("OAuth2 exchange: %w", err)
	}
	var tok OAuth2Token
	if err := json.Unmarshal([]byte(body), &tok); err != nil {
		return nil, fmt.Errorf("parsing OAuth2 token: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("OAuth2 exchange returned no access token (body: %.120q)", body)
	}
	tok.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return &tok, nil
}

// oauth1Header builds an OAuth 1.0a HMAC-SHA1 Authorization header for a request.
// token/tokenSecret are empty for the preauthorize (consumer-only) call.
func (c *Client) oauth1Header(method, rawURL string, extra url.Values, token, tokenSecret string) string {
	params := map[string]string{
		"oauth_consumer_key":     c.consumer.Key,
		"oauth_nonce":            nonce(),
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        strconv.FormatInt(time.Now().Unix(), 10),
		"oauth_version":          "1.0",
	}
	if token != "" {
		params["oauth_token"] = token
	}

	u, _ := url.Parse(rawURL)
	// Signature base includes query params and any extra body params.
	signParams := map[string]string{}
	for k, v := range params {
		signParams[k] = v
	}
	for k, vs := range u.Query() {
		signParams[k] = vs[0]
	}
	for k, vs := range extra {
		signParams[k] = vs[0]
	}

	baseURL := u.Scheme + "://" + u.Host + u.Path
	signature := sign(method, baseURL, signParams, c.consumer.Secret, tokenSecret)
	params["oauth_signature"] = signature

	var pairs []string
	for k, v := range params {
		pairs = append(pairs, fmt.Sprintf("%s=%q", oauthEscape(k), oauthEscape(v)))
	}
	sort.Strings(pairs)
	return "OAuth " + strings.Join(pairs, ", ")
}

// sign computes the OAuth1 HMAC-SHA1 signature over the canonical base string.
func sign(method, baseURL string, params map[string]string, consumerSecret, tokenSecret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, oauthEscape(k)+"="+oauthEscape(params[k]))
	}
	base := strings.ToUpper(method) + "&" + oauthEscape(baseURL) + "&" + oauthEscape(strings.Join(pairs, "&"))
	key := oauthEscape(consumerSecret) + "&" + oauthEscape(tokenSecret)
	mac := hmac.New(sha1.New, []byte(key))
	mac.Write([]byte(base))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// --- low-level HTTP helpers ---------------------------------------------------

func (c *Client) get(ctx context.Context, rawURL, ua, referer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	applyBrowserHeaders(req, referer)
	body, _, err := c.do(req)
	return body, err
}

func (c *Client) postForm(ctx context.Context, rawURL string, form url.Values, ua, referer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	applyBrowserHeaders(req, referer)
	// Garmin's SSO rejects the credential POST as a "Forbidden Exception" unless
	// it carries a same-origin Origin (a server-side cross-origin guard).
	req.Header.Set("Origin", ssoBase)
	body, _, err := c.do(req)
	return body, err
}

// applyBrowserHeaders sets the Accept/Accept-Language/Sec-Fetch/Referer headers a
// real browser sends, which Garmin's SSO checks on the login POST. A referer of
// "" means a top-level navigation (Sec-Fetch-Site: none).
func applyBrowserHeaders(req *http.Request, referer string) {
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-User", "?1")
	if referer != "" {
		req.Header.Set("Referer", referer)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	} else {
		req.Header.Set("Sec-Fetch-Site", "none")
	}
}

// do executes a request and returns the body, rejecting non-2xx/3xx responses.
func (c *Client) do(req *http.Request) (string, int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", resp.StatusCode, fmt.Errorf("reading response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return string(body), resp.StatusCode, fmt.Errorf("garmin returned HTTP %d for %s", resp.StatusCode, req.URL.Path)
	}
	return string(body), resp.StatusCode, nil
}

// --- pure helpers -------------------------------------------------------------

func mfaRequested(html string) bool {
	if m := titleRe.FindStringSubmatch(html); m != nil {
		return strings.Contains(strings.ToLower(m[1]), "mfa")
	}
	return strings.Contains(strings.ToLower(html), "enter mfa code")
}

func extract(re *regexp.Regexp, body, what string) (string, error) {
	m := re.FindStringSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("could not find %s in Garmin response", what)
	}
	return m[1], nil
}

func nonce() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + strconv.FormatInt(time.Now().Unix(), 36)
}

// oauthEscape percent-encodes per RFC 5849 (OAuth1): unreserved chars are
// A-Za-z0-9-._~; everything else is %XX uppercase.
func oauthEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '.' || ch == '_' || ch == '~' {
			b.WriteByte(ch)
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}
