package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/stuffam/drawbridge/internal/views"
)

// Client talks to the daemon's control socket.
type Client struct {
	socket string
	http   *http.Client
}

// NewClient returns a Client for the socket at path.
func NewClient(path string) *Client {
	return &Client{
		socket: path,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", path)
				},
			},
		},
	}
}

// Error is an error the daemon returned.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://drawbridge"+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return c.dialError(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e views.Error
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("the daemon returned %s", resp.Status)
		}
		return &Error{Status: resp.StatusCode, Message: e.Error}
	}
	if s, ok := out.(*string); ok {
		*s = string(data)
		return nil
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) dialError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("can't reach the Drawbridge daemon at %s; is drawbridge.service running?", c.socket)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("no permission to use %s; run as root (sudo) or as a member of the drawbridge group", c.socket)
	}
	return err
}

func clientPath(name string, suffix string) string {
	return "/v1/clients/" + url.PathEscape(name) + suffix
}

// Settings returns the server's settings.
func (c *Client) Settings(ctx context.Context) (views.SettingsView, error) {
	var v views.SettingsView
	return v, c.do(ctx, http.MethodGet, "/v1/settings", nil, &v)
}

// BackupInfo describes a backup the daemon sent.
type BackupInfo struct {
	// Name is the file name the daemon suggests, with the time in it.
	Name string
	Size int64
}

// Backup asks the daemon for an encrypted backup, and copies it to w. The file isn't in the
// daemon's hands after it's sent. It has no overall timeout, because a big database takes as long
// as it takes; ctx ends it.
func (c *Client) Backup(ctx context.Context, passphrase string, w io.Writer) (BackupInfo, error) {
	b, err := json.Marshal(views.BackupRequest{Passphrase: passphrase})
	if err != nil {
		return BackupInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://drawbridge/v1/backup", bytes.NewReader(b))
	if err != nil {
		return BackupInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := *c.http
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return BackupInfo{}, c.dialError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var e views.Error
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("the daemon returned %s", resp.Status)
		}
		return BackupInfo{}, &Error{Status: resp.StatusCode, Message: e.Error}
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return BackupInfo{}, err
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return BackupInfo{}, fmt.Errorf("the backup was cut short: %d of %d bytes", n, resp.ContentLength)
	}
	info := BackupInfo{Size: n}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		info.Name = filepath.Base(params["filename"])
	}
	return info, nil
}

// Diagnostics runs the daemon's host checks (`drawbridge doctor`).
func (c *Client) Diagnostics(ctx context.Context) (views.Diagnostics, error) {
	var v views.Diagnostics
	return v, c.do(ctx, http.MethodGet, "/v1/diagnostics", nil, &v)
}

// Certificate describes the TLS certificate the web UI is serving.
func (c *Client) Certificate(ctx context.Context) (views.Certificate, error) {
	var v views.Certificate
	return v, c.do(ctx, http.MethodGet, "/v1/tls", nil, &v)
}

// InstallCertificate makes a certificate chain and its private key (PEM) the web UI's.
func (c *Client) InstallCertificate(ctx context.Context, certPEM, keyPEM string) (views.Certificate, error) {
	var v views.Certificate
	return v, c.do(ctx, http.MethodPut, "/v1/tls", views.CertificateInstallRequest{Certificate: certPEM, PrivateKey: keyPEM}, &v)
}

// ResetCertificate goes back to the self-signed certificate.
func (c *Client) ResetCertificate(ctx context.Context) (views.Certificate, error) {
	var v views.Certificate
	return v, c.do(ctx, http.MethodDelete, "/v1/tls", nil, &v)
}

// DNSCheck asks the server's VPN addresses whether a DNS resolver answers on them.
func (c *Client) DNSCheck(ctx context.Context) (views.DNSCheck, error) {
	var v views.DNSCheck
	return v, c.do(ctx, http.MethodGet, "/v1/dns-check", nil, &v)
}

// Pending returns the settings change waiting to be kept; its PendingChange is nil when none is.
func (c *Client) Pending(ctx context.Context) (views.ApplyState, error) {
	var v views.ApplyState
	return v, c.do(ctx, http.MethodGet, "/v1/pending", nil, &v)
}

// ConfirmChange keeps the settings change that's waiting.
func (c *Client) ConfirmChange(ctx context.Context) (views.SettingsResult, error) {
	var v views.SettingsResult
	return v, c.do(ctx, http.MethodPost, "/v1/pending/confirm", nil, &v)
}

// RevertChange undoes the settings change that's waiting, now.
func (c *Client) RevertChange(ctx context.Context) (views.SettingsResult, error) {
	var v views.SettingsResult
	return v, c.do(ctx, http.MethodPost, "/v1/pending/revert", nil, &v)
}

// Apply reconciles once, or with dryRun says what it would change.
func (c *Client) Apply(ctx context.Context, dryRun bool) (views.ApplyResult, error) {
	path := "/v1/apply"
	if dryRun {
		path += "?dry_run=1"
	}
	var v views.ApplyResult
	return v, c.do(ctx, http.MethodPost, path, nil, &v)
}

// UpdateSettingsSafely is UpdateSettings that puts a change that could lock the admin out on
// probation: it's undone unless ConfirmChange keeps it in time.
func (c *Client) UpdateSettingsSafely(ctx context.Context, p views.SettingsPatch) (views.SettingsResult, error) {
	var v views.SettingsResult
	return v, c.do(ctx, http.MethodPatch, "/v1/settings?safe=1", p, &v)
}

// RotateServerKey gives the server a new key pair, which cuts off every client until it has its
// new config. With safe, the rotation is undone unless ConfirmChange keeps it in time.
func (c *Client) RotateServerKey(ctx context.Context, safe bool) (views.SettingsResult, error) {
	path := "/v1/server/rotate-key"
	if safe {
		path += "?safe=1"
	}
	var v views.SettingsResult
	return v, c.do(ctx, http.MethodPost, path, nil, &v)
}

// UpdateSettings applies a patch.
func (c *Client) UpdateSettings(ctx context.Context, p views.SettingsPatch) (views.SettingsResult, error) {
	var v views.SettingsResult
	return v, c.do(ctx, http.MethodPatch, "/v1/settings", p, &v)
}

// Clients lists every client with its live status.
func (c *Client) Clients(ctx context.Context) ([]views.ClientView, error) {
	var v []views.ClientView
	return v, c.do(ctx, http.MethodGet, "/v1/clients", nil, &v)
}

// Client returns one client with its live status.
func (c *Client) Client(ctx context.Context, name string) (views.ClientView, error) {
	var v views.ClientView
	return v, c.do(ctx, http.MethodGet, clientPath(name, ""), nil, &v)
}

// AddClient creates a client.
func (c *Client) AddClient(ctx context.Context, name string) (views.ClientResult, error) {
	var v views.ClientResult
	return v, c.do(ctx, http.MethodPost, "/v1/clients", views.NewClientRequest{Name: name}, &v)
}

// SetEnabled pauses (false) or resumes (true) a client.
func (c *Client) SetEnabled(ctx context.Context, name string, enabled bool) (views.ClientResult, error) {
	action := "/pause"
	if enabled {
		action = "/resume"
	}
	var v views.ClientResult
	return v, c.do(ctx, http.MethodPost, clientPath(name, action), nil, &v)
}

// RotateClientKeys gives a client new keys.
func (c *Client) RotateClientKeys(ctx context.Context, name string) (views.ClientResult, error) {
	var v views.ClientResult
	return v, c.do(ctx, http.MethodPost, clientPath(name, "/rotate-keys"), nil, &v)
}

// DeleteClient deletes a client.
func (c *Client) DeleteClient(ctx context.Context, name string) (views.ClientResult, error) {
	var v views.ClientResult
	return v, c.do(ctx, http.MethodDelete, clientPath(name, ""), nil, &v)
}

// Config returns a client's WireGuard config.
func (c *Client) Config(ctx context.Context, name string) (string, error) {
	var s string
	return s, c.do(ctx, http.MethodGet, clientPath(name, "/config"), nil, &s)
}

// RenameClient renames a client.
func (c *Client) RenameClient(ctx context.Context, name, newName string) (views.ClientResult, error) {
	var v views.ClientResult
	return v, c.do(ctx, http.MethodPatch, clientPath(name, ""), views.ClientPatch{Name: &newName}, &v)
}

// Events returns recorded events, newest first. client ("" for all) is a client's name.
func (c *Client) Events(ctx context.Context, client string, limit int) ([]views.EventView, error) {
	q := url.Values{}
	if client != "" {
		q.Set("client", client)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var v []views.EventView
	return v, c.do(ctx, http.MethodGet, "/v1/events?"+q.Encode(), nil, &v)
}

// SetupToken returns the first-run setup token and the web UI certificate's
// fingerprint.
func (c *Client) SetupToken(ctx context.Context) (SetupTokenResult, error) {
	var v SetupTokenResult
	return v, c.do(ctx, http.MethodGet, "/v1/admin/setup-token", nil, &v)
}

// CreateAdmin creates the admin account and returns its random password.
func (c *Client) CreateAdmin(ctx context.Context, username string) (AdminResult, error) {
	var v AdminResult
	return v, c.do(ctx, http.MethodPost, "/v1/admin/create", AdminRequest{Username: username}, &v)
}

// ResetPassword gives the admin account a new random password ("" for the only account).
func (c *Client) ResetPassword(ctx context.Context, username string) (AdminResult, error) {
	var v AdminResult
	return v, c.do(ctx, http.MethodPost, "/v1/admin/reset-password", AdminRequest{Username: username}, &v)
}
