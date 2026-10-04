package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/stuffam/drawbridge/internal/control"
	"github.com/stuffam/drawbridge/internal/views"
)

const tlsUsage = `Usage: drawbridge tls <command> [flags]

  show
        Show the TLS certificate the web UI is serving: the self-signed one the daemon makes,
        or one you installed. Includes its SHA-256 fingerprint, which a browser shows too.
  install --cert FILE --key FILE
        Serve your own certificate instead of the self-signed one. FILE is PEM text: the
        certificate chain (the server's own certificate first, then the intermediates, as a
        "fullchain" file has it) and its private key, which must not be protected by a
        passphrase. One file that holds both can be given for each. Both are checked first,
        and nothing changes if either is refused. New connections use it at once; the daemon
        doesn't restart. Nothing renews it: install a new one before it expires.
  reset
        Go back to the self-signed certificate, and forget the installed one.

Flags:
`

// maxCertFile is the most the command reads from a file: a long chain and its key come to a
// few kilobytes, and the daemon takes a request of 64 KiB.
const maxCertFile = 60 << 10

func tlsCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, tlsUsage)
		return 2
	}
	sub := args[0]
	if sub != "show" && sub != "install" && sub != "reset" {
		fmt.Fprintf(stderr, "drawbridge tls: unknown command %q\n\n%s", sub, tlsUsage)
		return 2
	}
	flags := newFlagSet("tls "+sub, stderr)
	socket := flags.String("control", defaultControl, "daemon control socket `path`")
	var certFile, keyFile *string
	if sub == "install" {
		certFile = flags.String("cert", "", "the certificate chain, `file` of PEM text")
		keyFile = flags.String("key", "", "the private key, `file` of PEM text without a passphrase")
	}
	flags.Usage = func() { fmt.Fprint(stderr, tlsUsage); flags.PrintDefaults() }
	pos, err := parseArgs(flags, args[1:])
	if err != nil {
		return flagStatus(err)
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "drawbridge tls %s: unexpected argument %q\n", sub, pos[0])
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "drawbridge:", err)
		return 1
	}
	c := control.NewClient(*socket)
	switch sub {
	case "show":
		cert, err := c.Certificate(ctx)
		if err != nil {
			return fail(err)
		}
		printCertificate(stdout, cert, time.Now())
	case "install":
		if *certFile == "" || *keyFile == "" {
			fmt.Fprint(stderr, "drawbridge tls install: --cert and --key are both needed\n\n")
			flags.Usage()
			return 2
		}
		certPEM, err := readCertFile(*certFile)
		if err != nil {
			return fail(err)
		}
		keyPEM := certPEM
		if *keyFile != *certFile {
			if keyPEM, err = readCertFile(*keyFile); err != nil {
				return fail(err)
			}
		}
		cert, err := c.InstallCertificate(ctx, certPEM, keyPEM)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "The web UI now serves your certificate. New connections use it; the daemon didn't restart.")
		fmt.Fprintln(stdout)
		printCertificate(stdout, cert, time.Now())
	case "reset":
		cert, err := c.ResetCertificate(ctx)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "The web UI is back on its self-signed certificate. New connections use it.")
		fmt.Fprintln(stdout)
		printCertificate(stdout, cert, time.Now())
	}
	return 0
}

// readCertFile reads a PEM file, which a certificate and its key never make large.
func readCertFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is the admin's own flag.
	if err != nil {
		return "", err
	}
	if len(data) > maxCertFile {
		return "", fmt.Errorf("%s is bigger than %d KiB, far more than a certificate and its key; is it the right file?", path, maxCertFile>>10)
	}
	return string(data), nil
}

// printCertificate writes what the admin should know about the certificate in use.
func printCertificate(w io.Writer, c views.Certificate, now time.Time) {
	source := "self-signed by Drawbridge"
	if c.Source == "uploaded" {
		source = "installed by you"
	}
	left := c.NotAfter.Sub(now)
	valid := fmt.Sprintf("%s to %s", c.NotBefore.Format(time.DateOnly), c.NotAfter.Format(time.DateOnly))
	if left > 0 {
		valid += fmt.Sprintf(" (%s left)", count(int(left/(24*time.Hour)), "day", "days"))
	} else {
		valid += " (expired)"
	}
	chain := count(c.Chain, "certificate", "certificates")
	fmt.Fprintf(w, "Certificate: %s\n", source)
	fmt.Fprintf(w, "Subject:     %s\n", c.Subject)
	fmt.Fprintf(w, "Issuer:      %s\n", c.Issuer)
	fmt.Fprintf(w, "Names:       %s\n", strings.Join(c.Names, ", "))
	fmt.Fprintf(w, "Valid:       %s\n", valid)
	fmt.Fprintf(w, "SHA-256:     %s\n", c.Fingerprint)
	fmt.Fprintf(w, "Chain:       %s\n", chain)
	for _, n := range c.Notes {
		fmt.Fprintln(w)
		for i, line := range wrapText("Note: "+n, doctorWrap) {
			if i > 0 {
				line = "      " + line
			}
			fmt.Fprintln(w, line)
		}
	}
}
