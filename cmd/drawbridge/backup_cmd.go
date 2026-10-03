package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/stuffam/drawbridge/internal/backup"
	"github.com/stuffam/drawbridge/internal/control"
)

const backupUsage = `Usage: drawbridge backup <command> [flags]

  create [--output FILE]
        Make an encrypted backup: a snapshot of the database and the secret key it's
        sealed with, in one file. It's encrypted with a passphrase you choose, which is the
        only thing that protects it, so make it a long one. Keep the file somewhere other
        than this host: it's what brings the VPN back after the SD card fails.
  restore FILE
        Put a backup in place of this host's database and key. Run it as root, with the
        daemon stopped (sudo systemctl stop drawbridge.service). Nothing is changed until
        the backup has been checked, and what it replaces is kept beside it.

Flags:
`

// A hook for the tests, which have no terminal.
var (
	isTerminal   = term.IsTerminal
	readPassword = term.ReadPassword
)

func backupCmd(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, backupUsage)
		return 2
	}
	sub := args[0]
	if sub != "create" && sub != "restore" {
		fmt.Fprintf(stderr, "drawbridge backup: unknown command %q\n\n%s", sub, backupUsage)
		return 2
	}
	flags := newFlagSet("backup "+sub, stderr)
	socket := flags.String("control", defaultControl, "daemon control socket `path`")
	passFile := flags.String("passphrase-file", "", "read the passphrase from this `file` (mode 0600), not the terminal")
	var output, db, secret, owner *string
	if sub == "create" {
		output = flags.String("output", "", "write the backup to this `file` (default: a dated name in this directory)")
	} else {
		db = flags.String("db", defaultDB, "the database `file` to replace")
		secret = flags.String("secret-key", defaultSecret, "the secret key `file` to replace")
		owner = flags.String("owner", "drawbridge", "the `user` that runs the daemon, who gets the files (\"none\" leaves them as they're made)")
	}
	flags.Usage = func() { fmt.Fprint(stderr, backupUsage); flags.PrintDefaults() }
	pos, err := parseArgs(flags, args[1:])
	if err != nil {
		return flagStatus(err)
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "drawbridge:", err)
		return 1
	}

	if sub == "create" {
		if len(pos) > 0 {
			fmt.Fprintf(stderr, "drawbridge backup create: unexpected argument %q\n", pos[0])
			return 2
		}
		return createBackup(ctx, control.NewClient(*socket), *output, *passFile, stdin, stdout, stderr, fail)
	}
	if len(pos) != 1 {
		fmt.Fprint(stderr, backupUsage)
		return 2
	}
	return restoreBackup(ctx, pos[0], restoreFlags{socket: *socket, passFile: *passFile, db: *db, secret: *secret, owner: *owner},
		stdin, stdout, stderr, fail)
}

func createBackup(ctx context.Context, c *control.Client, output, passFile string, stdin io.Reader, stdout, stderr io.Writer, fail func(error) int) int {
	pass, err := readPassphrase(stdin, stderr, passFile, true)
	if err != nil {
		return fail(err)
	}
	// Refused here, so a short passphrase doesn't cost a snapshot, or a second go at typing it.
	if _, err := backup.Check(pass); err != nil {
		return fail(err)
	}

	// The file is written beside where it's going, and moved into place when it's whole, so a
	// failure leaves no half a backup that looks like one.
	var f *os.File
	var partial string
	open := func(path string) error {
		partial = path + ".partial"
		var err error
		f, err = os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the path is the admin's own flag.
		return err
	}
	if output != "" {
		if _, err := os.Lstat(output); err == nil { //nolint:gosec // G703: the path is the admin's own flag.
			return fail(fmt.Errorf("%s already exists; choose another name with --output", output))
		}
		if err := open(output); err != nil {
			return fail(err)
		}
	} else {
		// The daemon names the file, with the time in it, so ask first and name it after.
		if err := open(fmt.Sprintf("drawbridge-backup-%d", os.Getpid())); err != nil {
			return fail(err)
		}
	}
	defer func() {
		if f != nil {
			_ = f.Close()
			_ = os.Remove(partial)
		}
	}()
	info, err := c.Backup(ctx, pass, f)
	if err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	f = nil
	final := output
	if final == "" {
		final = info.Name
		if final == "" {
			final = "drawbridge.backup"
		}
	}
	if _, err := os.Lstat(final); err == nil { //nolint:gosec // G703: the path is the admin's own flag.
		_ = os.Remove(partial)
		return fail(fmt.Errorf("%s already exists; the backup is gone, so run it again with --output", final))
	}
	if err := os.Rename(partial, final); err != nil { //nolint:gosec // G703: the path is the admin's own flag.
		_ = os.Remove(partial)
		return fail(err)
	}
	abs, err := filepath.Abs(final)
	if err != nil {
		abs = final
	}
	fmt.Fprintf(stdout, "Wrote the backup to %s (%s).\n", abs, humanSize(info.Size))
	fmt.Fprintln(stdout, "It holds every key this server has, and only the passphrase protects it. Keep it")
	fmt.Fprintln(stdout, "and the passphrase somewhere other than this host: without the passphrase it can't")
	fmt.Fprintln(stdout, "be opened, and nobody, including Drawbridge's developers, can recover it.")
	return 0
}

type restoreFlags struct {
	socket, passFile, db, secret, owner string
}

func restoreBackup(ctx context.Context, file string, fl restoreFlags, stdin io.Reader, stdout, stderr io.Writer, fail func(error) int) int {
	// A running daemon holds the database open and keeps writing to it, so replacing the file
	// under it would lose the restore, or damage it.
	if conn, err := net.DialTimeout("unix", fl.socket, time.Second); err == nil {
		_ = conn.Close()
		return fail(errors.New("the Drawbridge daemon is running. Stop it first:\n  sudo systemctl stop drawbridge.service"))
	}
	var chown func(path string, key bool) error
	if fl.owner != "none" {
		if os.Geteuid() != 0 {
			return fail(errors.New("restoring replaces files that belong to root and to the drawbridge user, so run it as root (sudo)"))
		}
		u, err := user.Lookup(fl.owner)
		if err != nil {
			return fail(fmt.Errorf("can't find the user %q, who should own the database: %w (use --owner to name another, or --owner none)", fl.owner, err))
		}
		uid, err1 := strconv.Atoi(u.Uid)
		gid, err2 := strconv.Atoi(u.Gid)
		if err1 != nil || err2 != nil {
			return fail(fmt.Errorf("the user %q has no numeric IDs", fl.owner))
		}
		chown = func(path string, key bool) error {
			// The database belongs to the daemon's user. The key belongs to root and is read by
			// the daemon's group, as the package installs it.
			owner := uid
			if key {
				owner = 0
			}
			return os.Chown(path, owner, gid)
		}
	}
	actor := "root"
	if u, err := user.Current(); err == nil {
		actor = u.Username
	}

	pass, err := readPassphrase(stdin, stderr, fl.passFile, false)
	if err != nil {
		return fail(err)
	}
	res, err := backup.Restore(ctx, backup.RestoreOptions{File: file, Passphrase: pass, DBPath: fl.db, KeyPath: fl.secret, Chown: chown, Actor: actor})
	if err != nil {
		return fail(restoreError(err))
	}

	m := res.Manifest
	fmt.Fprintf(stdout, "Restored the backup made %s", m.CreatedAt.UTC().Format("2 Jan 2006 15:04 UTC"))
	if m.Version != "" {
		fmt.Fprintf(stdout, " by Drawbridge v%s", m.Version)
	}
	fmt.Fprintln(stdout, ".")
	fmt.Fprintf(stdout, "  Database: %s\n  Key:      %s\n", fl.db, fl.secret)
	if res.Migrated {
		fmt.Fprintln(stdout, "  The database was from an older Drawbridge, and has been brought up to date.")
	}
	if res.SessionsEnded > 0 {
		fmt.Fprintf(stdout, "  Every login in it was ended (%d), so everyone logs in again.\n", res.SessionsEnded)
	}
	if res.DBAside != "" || res.KeyAside != "" {
		fmt.Fprintln(stdout, "Kept what it replaced:")
		if res.DBAside != "" {
			fmt.Fprintf(stdout, "  %s (with its -wal and -shm files, if it had them)\n", res.DBAside)
		}
		if res.KeyAside != "" {
			fmt.Fprintf(stdout, "  %s\n", res.KeyAside)
		}
		fmt.Fprintln(stdout, "Delete them once you've checked that everything works: the old key is in there.")
	}
	fmt.Fprintln(stdout, "Now start Drawbridge on the restored data:")
	fmt.Fprintln(stdout, "  sudo systemctl restart drawbridge-tunnel.service drawbridge.service")
	return 0
}

// restoreError says what went wrong with a restore in words for the admin, and that nothing
// was changed when that's so.
func restoreError(err error) error {
	switch {
	case errors.Is(err, backup.ErrPassphrase), errors.Is(err, backup.ErrDamaged), errors.Is(err, backup.ErrNotABackup),
		errors.Is(err, backup.ErrNewer), errors.Is(err, backup.ErrSchemaNewer):
		return fmt.Errorf("%w. Nothing was changed", err)
	}
	return fmt.Errorf("%w. If the files had been moved, they were put back", err)
}

// readPassphrase reads a backup passphrase from a file, from the terminal with nothing echoed
// (twice when confirm is set, to catch a typo that would make the backup unopenable), or, when
// there's no terminal, from the first line of stdin.
func readPassphrase(stdin io.Reader, stderr io.Writer, file string, confirm bool) (string, error) {
	if file != "" {
		info, err := os.Stat(file) //nolint:gosec // G703: the path is the admin's own flag.
		if err != nil {
			return "", err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("%s can be read by other users (mode %s); run: chmod 600 %s", file, info.Mode().Perm(), file)
		}
		b, err := os.ReadFile(file) //nolint:gosec // G304: the path is the admin's own flag.
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"), nil
	}
	if f, ok := stdin.(*os.File); ok && isTerminal(int(f.Fd())) {
		fd := int(f.Fd())
		fmt.Fprint(stderr, "Backup passphrase: ")
		first, err := readPassword(fd)
		fmt.Fprintln(stderr)
		if err != nil {
			return "", err
		}
		if confirm {
			fmt.Fprint(stderr, "Again: ")
			second, err := readPassword(fd)
			fmt.Fprintln(stderr)
			if err != nil {
				return "", err
			}
			if string(first) != string(second) {
				return "", errors.New("the two passphrases don't match")
			}
		}
		return string(first), nil
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if line == "" {
		return "", errors.New("no passphrase: give one on the terminal, with --passphrase-file, or as the first line of standard input")
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
