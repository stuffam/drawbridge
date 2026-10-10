package version

import (
	"testing"
	"time"
)

// local is a time in the host's time zone as the build stamps it, in UTC, so the day a test expects
// is the same wherever the tests run.
func local(year int, month time.Month, day, hour int) string {
	return time.Date(year, month, day, hour, 15, 46, 0, time.Local).UTC().Format(time.RFC3339)
}

// The text is the one the web UI's footer shows after the name (web/src/lib/version.ts).
func TestFormat(t *testing.T) {
	tests := []struct {
		name                string
		version, commit, at string
		want                string
	}{
		{"all three", "1.2.3", "abc1234", local(2026, time.October, 7, 21), "v1.2.3 (abc1234, 7 Oct 2026)"},
		{"a day with one digit has no zero", "1.2.3", "abc1234", local(2027, time.January, 5, 8), "v1.2.3 (abc1234, 5 Jan 2027)"},
		{"a build that couldn't tell the date", "1.2.3", "abc1234", "", "v1.2.3 (abc1234)"},
		{"a date it can't read isn't printed", "1.2.3", "abc1234", "yesterday", "v1.2.3 (abc1234)"},
		{"a build with nothing stamped", "0.0.0-dev", "unknown", "", "v0.0.0-dev (unknown)"},
		{"a dirty tree", "0.0.0-dev", "4e52c6d-dirty", local(2026, time.October, 10, 16), "v0.0.0-dev (4e52c6d-dirty, 10 Oct 2026)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := format(tt.version, tt.commit, tt.at); got != tt.want {
				t.Errorf("format(%q, %q, %q) = %q, want %q", tt.version, tt.commit, tt.at, got, tt.want)
			}
		})
	}
}

// String reads what the build stamped, and nothing else.
func TestStringUsesTheStampedValues(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldVersion, oldCommit, oldDate })

	Version, Commit, Date = "0.1.0-rc.2", "4e52c6d", local(2026, time.October, 7, 21)
	if got, want := String(), "v0.1.0-rc.2 (4e52c6d, 7 Oct 2026)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
