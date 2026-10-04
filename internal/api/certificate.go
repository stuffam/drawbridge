package api

import (
	"net/http"

	"github.com/stuffam/drawbridge/internal/views"
)

// certificate describes the TLS certificate the web UI is serving (docs/PLAN.md §6.6).
func (h *handler) certificate(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Certificate(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewCertificate(c))
}

// installCertificate makes the admin's own certificate the web UI's. It takes the account's
// password again, because a session alone (a hijacked one, a browser left open) mustn't be able
// to put in front of the admin's next login a certificate whose key it holds. The key travels in
// the request and is never kept in the response, the event, or the log.
func (h *handler) installCertificate(w http.ResponseWriter, r *http.Request) {
	var req views.CertificateInstallRequest
	if err := decode(w, r, &req); err != nil {
		h.fail(w, err)
		return
	}
	c, err := h.svc.InstallCertificateFor(r.Context(), sessionFrom(r.Context()).user, req.Password, req.Certificate, req.PrivateKey)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewCertificate(c))
}

// resetCertificate goes back to the self-signed certificate. It needs no password: it hands the
// admin's browser a warning, and no one a way in.
func (h *handler) resetCertificate(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.ResetCertificate(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewCertificate(c))
}
