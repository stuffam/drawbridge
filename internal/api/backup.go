package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/stuffam/drawbridge/internal/views"
)

// downloadWriteTimeout is how long one piece of a download may take to leave. The server has no
// write timeout (docs/adr/0009-server-sent-events.md), so a download sets one per write, and a
// reader that has stopped is dropped instead of holding the file, and the daemon's shutdown, open.
const downloadWriteTimeout = 30 * time.Second

var errShuttingDown = errors.New("the daemon is shutting down")

// downloadBackup makes a backup and sends it (docs/PLAN.md §6.6). It takes the account's password
// again, because the file holds the secret key beside the database it unlocks, and the
// passphrase that encrypts it. Neither is kept or logged. The whole file is made before any of it
// is sent, so a failure is an error response and never a download that stops partway.
//
// Restore isn't here, and never will be: it replaces the database, the admin's password included,
// so only the root CLI does it.
func (h *handler) downloadBackup(w http.ResponseWriter, r *http.Request) {
	var req views.BackupDownloadRequest
	if err := decode(w, r, &req); err != nil {
		h.fail(w, err)
		return
	}
	b, err := h.svc.CreateBackupFor(r.Context(), sessionFrom(r.Context()).user, req.Password, req.Passphrase)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer b.Close()
	header := w.Header()
	header.Set("Content-Type", "application/octet-stream")
	header.Set("Content-Disposition", `attachment; filename="`+b.Name+`"`)
	header.Set("Content-Length", strconv.FormatInt(b.Size, 10))
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	// The length is announced, so a download that stops early is a failed one to the browser,
	// not a short file it saves.
	if err := sendFile(w, b, h.shutdown); err != nil {
		h.log.Info("a backup download stopped early", "err", err)
	}
}

// sendFile copies r to w a piece at a time, each with its own write deadline, and stops when
// shutdown closes.
func sendFile(w http.ResponseWriter, r io.Reader, shutdown <-chan struct{}) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		select {
		case <-shutdown:
			return errShuttingDown
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			_ = rc.SetWriteDeadline(time.Now().Add(downloadWriteTimeout))
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// snapshots lists the database snapshots the host keeps. They aren't downloadable: a snapshot is
// the database as it is on disk, with the admin's password hash in it, and unlike a backup it has
// no passphrase. The host's own backups directory is the place for them.
func (h *handler) snapshots(w http.ResponseWriter, _ *http.Request) {
	l, err := h.svc.ListSnapshots()
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views.NewSnapshotList(l))
}
