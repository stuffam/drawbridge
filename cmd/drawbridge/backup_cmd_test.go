package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuffam/drawbridge/internal/keys"
	"github.com/stuffam/drawbridge/internal/store"
)

const cliPassphrase = "a long enough passphrase"

// The disaster the backup is for: the old host is gone, a new one has nothing, and the file
// brings the clients back, with their keys, under a database that's sealed the way it was.
func TestBackupCreateAndRestoreCommands(t *testing.T) {
	env := newFakeEnv(t)
	key := bytes.Repeat([]byte{5}, keys.SecretSize)
	env.svc.SecretKeyPath = filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(env.svc.SecretKeyPath, key, 0o640); err != nil {
		t.Fatal(err)
	}
	startDaemon(t, env)
	sock := "--control=" + env.socket
	if r := runCLI("", "client", "add", "a phone", sock); r.code != 0 {
		t.Fatalf("client add: %+v", r)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "my.backup")

	// A short passphrase is refused before anything is asked of the daemon, and nothing is made.
	if r := runCLI("short\n", "backup", "create", "--output", file, sock); r.code != 1 || !strings.Contains(r.stderr, "at least 12 characters") {
		t.Fatalf("a short passphrase: %+v", r)
	}
	if r := runCLI("", "backup", "create", "--output", file, sock); r.code != 1 || !strings.Contains(r.stderr, "no passphrase") {
		t.Fatalf("no passphrase: %+v", r)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a refused backup left %v", entries)
	}

	r := runCLI(cliPassphrase+"\n", "backup", "create", "--output", file, sock)
	if r.code != 0 || !strings.Contains(r.stdout, "Wrote the backup to "+file) || !strings.Contains(r.stdout, "only the passphrase protects it") {
		t.Fatalf("create: %+v", r)
	}
	if strings.Contains(r.stdout+r.stderr, cliPassphrase) {
		t.Error("the passphrase was printed")
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the backup is %v, %v; want mode 0600", info, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %v; a partial file was left", entries)
	}
	// It won't overwrite one, and says so before asking the daemon for anything.
	if r := runCLI(cliPassphrase+"\n", "backup", "create", "--output", file, sock); r.code != 1 || !strings.Contains(r.stderr, "already exists") {
		t.Fatalf("overwriting: %+v", r)
	}

	// Restore is for a host whose daemon is stopped. While this one runs, it won't.
	newDB, newKey := filepath.Join(t.TempDir(), "drawbridge.db"), filepath.Join(t.TempDir(), "secret.key")
	args := func(extra ...string) []string {
		return append([]string{"backup", "restore", file, "--db", newDB, "--secret-key", newKey, "--owner", "none"}, extra...)
	}
	if r := runCLI(cliPassphrase+"\n", args(sock)...); r.code != 1 || !strings.Contains(r.stderr, "daemon is running") || !strings.Contains(r.stderr, "systemctl stop") {
		t.Fatalf("restoring under a running daemon: %+v", r)
	}
	if _, err := os.Stat(newDB); err == nil {
		t.Fatal("a restore went ahead under a running daemon")
	}

	gone := "--control=" + filepath.Join(t.TempDir(), "no-daemon.sock")
	if r := runCLI("not the passphrase\n", args(gone)...); r.code != 1 || !strings.Contains(r.stderr, "wrong passphrase") || !strings.Contains(r.stderr, "Nothing was changed") {
		t.Fatalf("a wrong passphrase: %+v", r)
	}
	if _, err := os.Stat(newDB); err == nil {
		t.Fatal("a wrong passphrase restored something")
	}
	r = runCLI(cliPassphrase+"\n", args(gone)...)
	if r.code != 0 || !strings.Contains(r.stdout, "Restored the backup made") || !strings.Contains(r.stdout, "systemctl restart drawbridge-tunnel.service drawbridge.service") {
		t.Fatalf("restore: %+v", r)
	}

	// The restored host has the client, with the key it had, and the key that opens it.
	if got, _ := os.ReadFile(newKey); !bytes.Equal(got, key) {
		t.Fatal("the restored key isn't the backed-up one")
	}
	sealer, _ := keys.NewSealer(key)
	st, err := store.Open(context.Background(), newDB, sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	want, _ := env.store.Client(context.Background(), store.ByName("a phone"))
	got, err := st.Client(context.Background(), store.ByName("a phone"))
	if err != nil || got.ID != want.ID || *got.PrivateKey != *want.PrivateKey {
		t.Fatalf("the restored client %+v, %v; want %+v", got, err, want)
	}
}

func TestBackupCommandUsageAndErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no command":             {"backup"},
		"an unknown command":     {"backup", "frobnicate"},
		"restore with no file":   {"backup", "restore"},
		"restore with two files": {"backup", "restore", "a", "b"},
	} {
		if r := runCLI("", args...); r.code != 2 || !strings.Contains(r.stderr, "Usage: drawbridge backup") {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r := runCLI("", "backup", "create", "a"); r.code != 2 || !strings.Contains(r.stderr, `unexpected argument "a"`) {
		t.Errorf("create with an argument: %+v", r)
	}
	// Restoring needs root unless the files are left as they're made.
	if os.Geteuid() != 0 {
		r := runCLI(cliPassphrase+"\n", "backup", "restore", "x", "--control", filepath.Join(t.TempDir(), "none.sock"))
		if r.code != 1 || !strings.Contains(r.stderr, "run it as root") {
			t.Errorf("restore as a user: %+v", r)
		}
	}
	if r := runCLI(cliPassphrase+"\n", "backup", "restore", filepath.Join(t.TempDir(), "missing"), "--owner", "none",
		"--db", filepath.Join(t.TempDir(), "d"), "--secret-key", filepath.Join(t.TempDir(), "k"),
		"--control", filepath.Join(t.TempDir(), "none.sock")); r.code != 1 {
		t.Errorf("a missing backup: %+v", r)
	}
}

func TestReadPassphrase(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var errOut bytes.Buffer

	// A file, with its line ending dropped and nothing else.
	for content, want := range map[string]string{"pass phrase here\n": "pass phrase here", "pass phrase here\r\n": "pass phrase here", "no newline": "no newline", " spaces kept \n": " spaces kept "} {
		got, err := readPassphrase(strings.NewReader(""), &errOut, write("p", content, 0o600), false)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", content, got, err, want)
		}
	}
	// A file others can read is refused, like the secret key is.
	if _, err := readPassphrase(strings.NewReader(""), &errOut, write("open", "x", 0o644), false); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a world-readable passphrase file: %v", err)
	}
	// Standard input, when it isn't a terminal: its first line.
	if got, err := readPassphrase(strings.NewReader("first line\nsecond\n"), &errOut, "", true); err != nil || got != "first line" {
		t.Errorf("stdin: %q, %v", got, err)
	}
	if _, err := readPassphrase(strings.NewReader(""), &errOut, "", false); err == nil {
		t.Error("an empty stdin gave a passphrase")
	}
}

// On a terminal the passphrase is read without echo, and asked twice when a typo would make a
// backup nobody can open.
func TestReadPassphraseOnATerminal(t *testing.T) {
	tty, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	oldIs, oldRead := isTerminal, readPassword
	t.Cleanup(func() { isTerminal, readPassword = oldIs, oldRead })
	isTerminal = func(int) bool { return true }
	typed := []string{}
	readPassword = func(int) ([]byte, error) {
		p := typed[0]
		typed = typed[1:]
		return []byte(p), nil
	}

	var errOut bytes.Buffer
	typed = []string{"the same twice", "the same twice"}
	if got, err := readPassphrase(tty, &errOut, "", true); err != nil || got != "the same twice" {
		t.Errorf("matching: %q, %v", got, err)
	}
	if !strings.Contains(errOut.String(), "Again:") {
		t.Errorf("it didn't ask again: %q", errOut.String())
	}
	typed = []string{"one thing typed", "another thing"}
	if _, err := readPassphrase(tty, &errOut, "", true); err == nil || !strings.Contains(err.Error(), "don't match") {
		t.Errorf("mismatched: %v", err)
	}
	// Restoring asks once: a wrong one is refused by the backup itself.
	errOut.Reset()
	typed = []string{"only once"}
	if got, err := readPassphrase(tty, &errOut, "", false); err != nil || got != "only once" || strings.Contains(errOut.String(), "Again") {
		t.Errorf("restore prompt: %q, %v, %q", got, err, errOut.String())
	}
}
