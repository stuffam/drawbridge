// Package version holds the version information stamped into the binary at build time.
package version

import "time"

// Version is the bare release version, such as "1.2.3". The Makefile sets it with
// -ldflags "-X github.com/stuffam/drawbridge/internal/version.Version=1.2.3".
var Version = "0.0.0-dev"

// Commit is the short git commit the binary was built from.
var Commit = "unknown"

// Date is the time of that commit, in UTC as RFC 3339 ("2026-10-08T02:15:46Z"), or empty when the
// build couldn't tell. It comes from the commit and not from the clock, so a build is still a
// function of its commit (docs/PLAN.md §11.1).
var Date = ""

// String returns the version as people see it, such as "v1.2.3 (abc1234, 7 Oct 2026)". It's the
// text the web UI's footer shows after the name (web/src/lib/version.ts).
func String() string {
	return format(Version, Commit, Date)
}

// format is String's work, apart from reading the stamped values. The day is the commit's in the
// host's time zone, as the web UI shows it in the browser's, and it's left out when the build has
// none or it can't be read.
func format(ver, commit, date string) string {
	s := "v" + ver + " (" + commit
	if t, err := time.Parse(time.RFC3339, date); err == nil {
		s += ", " + t.Local().Format("2 Jan 2006")
	}
	return s + ")"
}
