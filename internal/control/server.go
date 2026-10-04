// Package control is the daemon's local control socket, which the drawbridge CLI uses
// (docs/PLAN.md §4.1). It speaks JSON over HTTP on a Unix socket. Access is controlled by
// the socket's permissions: root and members of the drawbridge group.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/stuffam/drawbridge/internal/model"
	"github.com/stuffam/drawbridge/internal/service"
	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/views"
)

// DefaultSocket is the packaged daemon's control socket.
const DefaultSocket = "/run/drawbridge/control.sock"

// Listen listens on the Unix socket at path, replacing a stale socket from an earlier
// run. The socket is readable and writable by its owner and group (the drawbridge user
// and group) only.
func Listen(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("removing the old control socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on the control socket: %w", err)
	}
	// Group access is the point: members of the drawbridge group can use the CLI.
	if err := os.Chmod(path, 0o660); err != nil { //nolint:gosec // G302, see above.
		_ = ln.Close()
		return nil, fmt.Errorf("setting the control socket's permissions: %w", err)
	}
	return ln, nil
}

// NewServer returns the control socket's HTTP server. Each connection's changes are
// attributed to the account of the process on the other end, for the event log.
func NewServer(svc *service.Service, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           NewHandler(svc, log),
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return service.WithActor(ctx, service.Actor{Name: peerName(c), Via: service.ViaCLI})
		},
	}
}

// NewHandler returns the control API.
func NewHandler(svc *service.Service, log *slog.Logger) http.Handler {
	h := &handler{svc: svc, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/settings", h.getSettings)
	mux.HandleFunc("PATCH /v1/settings", h.patchSettings)
	mux.HandleFunc("GET /v1/dns-check", h.dnsCheck)
	mux.HandleFunc("POST /v1/server/rotate-key", h.rotateServerKey)
	mux.HandleFunc("GET /v1/pending", h.pendingChange)
	mux.HandleFunc("POST /v1/pending/confirm", h.confirmChange)
	mux.HandleFunc("POST /v1/pending/revert", h.revertChange)
	mux.HandleFunc("POST /v1/apply", h.apply)
	mux.HandleFunc("GET /v1/diagnostics", h.diagnostics)
	mux.HandleFunc("GET /v1/tls", h.certificate)
	mux.HandleFunc("PUT /v1/tls", h.installCertificate)
	mux.HandleFunc("DELETE /v1/tls", h.resetCertificate)
	mux.HandleFunc("GET /v1/clients", h.listClients)
	mux.HandleFunc("POST /v1/clients", h.addClient)
	mux.HandleFunc("GET /v1/clients/{name}", h.getClient)
	mux.HandleFunc("PATCH /v1/clients/{name}", h.patchClient)
	mux.HandleFunc("DELETE /v1/clients/{name}", h.deleteClient)
	mux.HandleFunc("POST /v1/clients/{name}/pause", h.setEnabled(false))
	mux.HandleFunc("POST /v1/clients/{name}/resume", h.setEnabled(true))
	mux.HandleFunc("POST /v1/clients/{name}/rotate-keys", h.rotateClientKeys)
	mux.HandleFunc("GET /v1/clients/{name}/config", h.clientConfig)
	mux.HandleFunc("POST /v1/backup", h.createBackup)
	mux.HandleFunc("GET /v1/events", h.events)
	mux.HandleFunc("GET /v1/admin/setup-token", h.setupToken)
	mux.HandleFunc("POST /v1/admin/create", h.createAdmin)
	mux.HandleFunc("POST /v1/admin/reset-password", h.resetPassword)
	mux.HandleFunc("POST /v1/admin/disable-2fa", h.disableTOTP)
	return mux
}

type handler struct {
	svc *service.Service
	log *slog.Logger
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// fail maps an error to a status: the caller's mistakes are 4xx, everything else 500.
func (h *handler) fail(w http.ResponseWriter, err error) {
	status := views.ErrorStatus(err)
	if status == http.StatusInternalServerError {
		h.log.Error("control request failed", "err", err)
	}
	writeJSON(w, status, views.Error{Error: err.Error()})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &model.InvalidError{Err: fmt.Errorf("bad request body: %w", err)}
	}
	return nil
}

func ref(r *http.Request) store.Ref { return store.ByName(r.PathValue("name")) }

func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.Settings(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.Settings(s))
}

func (h *handler) dnsCheck(w http.ResponseWriter, r *http.Request) {
	probes, err := h.svc.ProbeDNS(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewDNSCheck(probes))
}

func (h *handler) diagnostics(w http.ResponseWriter, r *http.Request) {
	checks, err := h.svc.Diagnose(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewDiagnostics(checks))
}

// certificate describes the TLS certificate the web UI is serving.
func (h *handler) certificate(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Certificate(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewCertificate(c))
}

// installCertificate makes the given certificate and key the web UI's. The socket is root's (or
// the drawbridge group's), so unlike the web API it doesn't ask for the account's password.
func (h *handler) installCertificate(w http.ResponseWriter, r *http.Request) {
	var req views.CertificateInstallRequest
	if err := decode(r, &req); err != nil {
		h.fail(w, err)
		return
	}
	c, err := h.svc.InstallCertificate(r.Context(), req.Certificate, req.PrivateKey)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewCertificate(c))
}

// resetCertificate goes back to the self-signed certificate.
func (h *handler) resetCertificate(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.ResetCertificate(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewCertificate(c))
}

// createBackup makes a backup and sends it. The passphrase comes in the body, which is only
// ever read here, and never logged.
func (h *handler) createBackup(w http.ResponseWriter, r *http.Request) {
	var req views.BackupRequest
	if err := decode(r, &req); err != nil {
		h.fail(w, err)
		return
	}
	b, err := h.svc.CreateBackup(r.Context(), req.Passphrase)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer b.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+b.Name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(b.Size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, b)
}

func (h *handler) patchSettings(w http.ResponseWriter, r *http.Request) {
	var p views.SettingsPatch
	if err := decode(r, &p); err != nil {
		h.fail(w, err)
		return
	}
	// The CLI applies at once, as the root user at the host can't be cut off by the change, unless
	// it asks for the change to wait to be kept (--safe), as the web UI always does.
	patch := p.Service()
	patch.SafeApply = r.URL.Query().Get("safe") == "1"
	s, applied, err := h.svc.UpdateSettings(r.Context(), patch)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewSettingsResult(s, applied))
}

// rotateServerKey gives the server a new key pair; with safe=1 it waits to be kept.
func (h *handler) rotateServerKey(w http.ResponseWriter, r *http.Request) {
	s, applied, err := h.svc.RotateServerKey(r.Context(), r.URL.Query().Get("safe") == "1")
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewSettingsResult(s, applied))
}

// pendingChange says whether a settings change is waiting to be kept.
func (h *handler) pendingChange(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.PendingChange(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.ApplyState{PendingChange: views.NewPendingChange(p)})
}

func (h *handler) confirmChange(w http.ResponseWriter, r *http.Request) {
	if _, err := h.svc.ConfirmChange(r.Context()); err != nil {
		h.fail(w, err)
		return
	}
	h.settingsResult(w, r, service.Applied{})
}

func (h *handler) revertChange(w http.ResponseWriter, r *http.Request) {
	_, applied, err := h.svc.RevertChange(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	h.settingsResult(w, r, applied)
}

func (h *handler) settingsResult(w http.ResponseWriter, r *http.Request, applied service.Applied) {
	s, err := h.svc.Settings(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewSettingsResult(s, applied))
}

// apply reconciles once (`drawbridge apply`), or with dry_run=1 says what it would change.
func (h *handler) apply(w http.ResponseWriter, r *http.Request) {
	dryRun := r.URL.Query().Get("dry_run") == "1"
	res, err := h.svc.Apply(r.Context(), dryRun)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewApplyResult(res, dryRun))
}

func (h *handler) listClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.svc.Clients(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.Statuses(clients))
}

func (h *handler) getClient(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Client(r.Context(), ref(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.Status(c))
}

func (h *handler) addClient(w http.ResponseWriter, r *http.Request) {
	var req views.NewClientRequest
	if err := decode(r, &req); err != nil {
		h.fail(w, err)
		return
	}
	c, applied, err := h.svc.AddClient(r.Context(), req.Name)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, views.NewClientResult(c, applied))
}

func (h *handler) patchClient(w http.ResponseWriter, r *http.Request) {
	var p views.ClientPatch
	if err := decode(r, &p); err != nil {
		h.fail(w, err)
		return
	}
	c, err := h.svc.Client(r.Context(), ref(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	if p.Name != nil {
		if c.Client, err = h.svc.RenameClient(r.Context(), store.ByID(c.ID), *p.Name); err != nil {
			h.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, views.ClientResult{Client: views.Status(c)})
}

func (h *handler) setEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, applied, err := h.svc.SetEnabled(r.Context(), ref(r), enabled)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, views.NewClientResult(c, applied))
	}
}

func (h *handler) rotateClientKeys(w http.ResponseWriter, r *http.Request) {
	c, applied, err := h.svc.RotateClientKeys(r.Context(), ref(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewClientResult(c, applied))
}

func (h *handler) deleteClient(w http.ResponseWriter, r *http.Request) {
	c, applied, err := h.svc.DeleteClient(r.Context(), ref(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewClientResult(c, applied))
}

func (h *handler) clientConfig(w http.ResponseWriter, r *http.Request) {
	_, conf, err := h.svc.Config(r.Context(), ref(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A plain-text config for the CLI on a Unix socket; no browser renders it.
	_, _ = io.WriteString(w, conf) //nolint:gosec // G705, see above.
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	f, err := views.ParseEventFilter(r.URL.Query())
	if err != nil {
		h.fail(w, err)
		return
	}
	if name := r.URL.Query().Get("client"); name != "" {
		c, err := h.svc.Client(r.Context(), store.ByName(name))
		if err != nil {
			h.fail(w, err)
			return
		}
		f.ClientID = c.ID
	}
	events, err := h.svc.Events(r.Context(), f)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.Events(events))
}

// SetupTokenResult is the pending first-run setup token, and the fingerprint of the
// certificate the browser will warn about. The fingerprint is empty when the web UI serves a
// certificate the admin installed, which a browser that trusts it doesn't warn about.
type SetupTokenResult struct {
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func (h *handler) setupToken(w http.ResponseWriter, r *http.Request) {
	token, err := h.svc.SetupToken(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, SetupTokenResult{Token: token, Fingerprint: h.svc.SetupFingerprint()})
}

// AdminRequest names the admin account.
type AdminRequest struct {
	Username string `json:"username"`
}

// AdminResult is an admin account's new password.
type AdminResult struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *handler) createAdmin(w http.ResponseWriter, r *http.Request) {
	var req AdminRequest
	if err := decode(r, &req); err != nil {
		h.fail(w, err)
		return
	}
	password, err := h.svc.CreateAdmin(r.Context(), req.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, AdminResult{Username: req.Username, Password: password})
}

func (h *handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req AdminRequest
	if err := decode(r, &req); err != nil {
		h.fail(w, err)
		return
	}
	u, password, err := h.svc.ResetPassword(r.Context(), req.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminResult{Username: u.Username, Password: password})
}

// TwoFactorResult says whether turning 2FA off changed anything.
type TwoFactorResult struct {
	Username string `json:"username"`
	// WasOn is false when 2FA was off already.
	WasOn bool `json:"was_on"`
}

func (h *handler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	var req AdminRequest
	if err := decode(r, &req); err != nil {
		h.fail(w, err)
		return
	}
	u, wasOn, err := h.svc.DisableTOTPFor(r.Context(), req.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, TwoFactorResult{Username: u.Username, WasOn: wasOn})
}

// peerName names the account of the process on the other end of a Unix socket: its
// username, or "uid N" if the account has no name.
func peerName(c net.Conn) string {
	uid, ok := peerUID(c)
	if !ok {
		return "unknown"
	}
	id := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(id); err == nil {
		return u.Username
	}
	return "uid " + id
}
