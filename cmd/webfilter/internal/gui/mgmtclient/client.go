// Package mgmtclient is the native desktop GUI's typed HTTP client for the
// management API. The GUI always goes through this client - even when it
// self-hosts the engine in the same process - so every write picks up the
// server-side coherence rules (MDM settings lock, policy audit logging,
// full-document policy updates, settings merge/validation) without a third
// copy of that logic. Keep this package free of gogpu imports: it is exercised
// headlessly by tests against a real mgmtapi.Server.
package mgmtclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"time"

	"github.com/yjlion/gowebfilter/internal/models"
)

// ErrManagedLocked maps the mgmt API's 403 "managed by your organization"
// response (mgmtapi.requireUnlocked). Screens flip to read-only when a write
// returns this.
var ErrManagedLocked = errors.New("settings are managed by your organization")

// ErrUnauthorized maps 401 responses - the GUI shows the login dialog.
var ErrUnauthorized = errors.New("not authenticated")

// APIError carries any other non-2xx response, preserving the server's
// {"detail": ...} message verbatim (settingsvc validation messages are
// user-facing and shown as-is).
type APIError struct {
	StatusCode int
	Detail     string
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("management API returned status %d", e.StatusCode)
}

// Status mirrors mgmtapi's GET /api/status response.
type Status struct {
	ProxyRunning   bool             `json:"proxy_running"`
	ProxyPort      int              `json:"proxy_port"`
	ProxyListen    []string         `json:"proxy_listen"`
	MgmtPort       int              `json:"mgmt_port"`
	RecentBlocks   []map[string]any `json:"recent_blocks"`
	RecentRequests []map[string]any `json:"recent_requests"`
	Tun2Socks      map[string]any   `json:"tun2socks"`
}

// AuthStatus mirrors GET /api/auth-status.
type AuthStatus struct {
	Enabled       bool `json:"enabled"`
	HasPassword   bool `json:"has_password"`
	Authenticated bool `json:"authenticated"`
}

// Client talks to one management server. Safe for concurrent use.
type Client struct {
	baseURL string
	httpc   *http.Client
}

// Option configures a Client.
type Option func(*Client) error

// WithSessionCookie seeds the cookie jar with a pre-minted session cookie
// (mgmtapi.Server.SessionCookie) so the self-hosted GUI never prompts the
// local owner for their own password.
func WithSessionCookie(name, value string) Option {
	return func(c *Client) error {
		return c.SetSessionCookie(name, value)
	}
}

// SetSessionCookie replaces the session cookie in the jar. The self-host
// supervisor calls this after every engine (re)start - a password change
// invalidates the previous deterministic token.
func (c *Client) SetSessionCookie(name, value string) error {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	c.httpc.Jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value}})
	return nil
}

// New builds a client for the management API at baseURL
// (e.g. "http://127.0.0.1:8000").
func New(baseURL string, opts ...Option) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &Client{
		baseURL: baseURL,
		httpc: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
		},
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// BaseURL returns the management server URL this client talks to (also what
// the "Open Web UI" button opens in the browser).
func (c *Client) BaseURL() string { return c.baseURL }

// do runs one request and decodes a 2xx JSON body into out (skipped when out
// is nil). Non-2xx responses become ErrManagedLocked / ErrUnauthorized /
// *APIError.
func (c *Client) do(method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Detail string `json:"detail"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		switch resp.StatusCode {
		case http.StatusForbidden:
			return fmt.Errorf("%w: %s", ErrManagedLocked, e.Detail)
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: %s", ErrUnauthorized, e.Detail)
		default:
			return &APIError{StatusCode: resp.StatusCode, Detail: e.Detail}
		}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// AuthStatus reports whether the server requires login and whether this
// client's cookie already authenticates.
func (c *Client) AuthStatus() (AuthStatus, error) {
	var st AuthStatus
	err := c.do(http.MethodGet, "/api/auth-status", nil, &st)
	return st, err
}

// Login exchanges the management password for a session cookie kept in the
// client's jar.
func (c *Client) Login(password string) error {
	return c.do(http.MethodPost, "/api/login", map[string]string{"password": password}, nil)
}

// Status fetches the dashboard payload.
func (c *Client) Status() (Status, error) {
	var st Status
	err := c.do(http.MethodGet, "/api/status", nil, &st)
	return st, err
}

// Settings fetches the current global settings. The response is the
// secret-stripped DTO; unmarshaling it into models.GlobalSettings leaves the
// secret fields empty, which is safe to PUT back - MergeSettings re-pins
// secrets server-side.
func (c *Client) Settings() (models.GlobalSettings, error) {
	var s models.GlobalSettings
	err := c.do(http.MethodGet, "/api/settings", nil, &s)
	return s, err
}

// UpdateSettings PUTs a settings document (full or partial - the server
// merges over current values). newPassword, when non-empty, travels as the
// separate new_password field the server hashes; raw hashes are never sent.
func (c *Client) UpdateSettings(s models.GlobalSettings, newPassword string) (models.GlobalSettings, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return models.GlobalSettings{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return models.GlobalSettings{}, err
	}
	// Never echo secret fields back, even empty - the server ignores them,
	// but the request should not carry keys named like secrets at all.
	delete(doc, "password_hash")
	delete(doc, "secret_key")
	delete(doc, "proxy_auth_password_hash")
	if newPassword != "" {
		doc["new_password"] = newPassword
	}
	var out models.GlobalSettings
	err = c.do(http.MethodPut, "/api/settings", doc, &out)
	return out, err
}

// Policies lists all policies (full documents, matching GET /api/policies).
func (c *Client) Policies() ([]models.Policy, error) {
	var list []models.Policy
	err := c.do(http.MethodGet, "/api/policies", nil, &list)
	return list, err
}

// Policy fetches one policy by name.
func (c *Client) Policy(name string) (models.Policy, error) {
	var p models.Policy
	err := c.do(http.MethodGet, "/api/policies/"+url.PathEscape(name), nil, &p)
	return p, err
}

// CreatePolicy creates a new policy from a full document.
func (c *Client) CreatePolicy(p models.Policy) (models.Policy, error) {
	var out models.Policy
	err := c.do(http.MethodPost, "/api/policies", p, &out)
	return out, err
}

// UpdatePolicy replaces the named policy with a full document (the only safe
// shape - partial bodies silently reset sub-config fields to defaults; see
// the CLAUDE.md gotcha). Renames happen by sending a different p.Name.
func (c *Client) UpdatePolicy(name string, p models.Policy) (models.Policy, error) {
	var out models.Policy
	err := c.do(http.MethodPut, "/api/policies/"+url.PathEscape(name), p, &out)
	return out, err
}

// DeletePolicy removes the named policy.
func (c *Client) DeletePolicy(name string) error {
	return c.do(http.MethodDelete, "/api/policies/"+url.PathEscape(name), nil, nil)
}

// Logs fetches the newest limit entries of kind "requests", "blocks", or
// "policy_changes".
func (c *Client) Logs(kind string, limit int) ([]map[string]any, error) {
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	err := c.do(http.MethodGet, "/api/logs?kind="+url.QueryEscape(kind)+"&limit="+strconv.Itoa(limit), nil, &out)
	return out.Entries, err
}
