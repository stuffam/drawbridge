package version

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Versions as the build stamps them (bare, without the v), with the entry of the docs site that
// matches each. The site keeps one entry for a minor release and none for a pre-release or a build
// between releases (scripts/docs-version.sh), so those read the development docs.
var docsCases = []struct {
	name, version, want string
}{
	{"a release", "0.1.0", "v0.1"},
	{"a patch release is its minor's", "0.1.3", "v0.1"},
	{"a later minor", "1.12.0", "v1.12"},
	{"a pre-release has no docs of its own", "0.1.0-rc.2", "dev"},
	{"a build past a release", "0.1.0+67", "dev"},
	{"a build past a pre-release", "0.1.0-rc.2+67", "dev"},
	{"a build with no tag", "0.0.0-dev", "dev"},
	{"nothing stamped", "", "dev"},
	{"a leading zero is not a version", "01.2.3", "dev"},
	{"the constant has no v", "v0.1.0", "dev"},
	{"a name that isn't a version", "0.1.0/../x", "dev"},
}

func TestDocsVersion(t *testing.T) {
	for _, tt := range docsCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := docsVersion(tt.version); got != tt.want {
				t.Errorf("docsVersion(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

// DocsURL reads what the build stamped, and builds the address of a page of the docs for it.
func TestDocsURLUsesTheStampedVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	for _, tt := range []struct{ version, want string }{
		{"0.1.3", "https://stuffam.github.io/drawbridge/v0.1/guides/diagnostics/"},
		{"0.1.0-rc.2+67", "https://stuffam.github.io/drawbridge/dev/guides/diagnostics/"},
		{"0.0.0-dev", "https://stuffam.github.io/drawbridge/dev/guides/diagnostics/"},
	} {
		Version = tt.version
		if got := DocsURL("guides/diagnostics/"); got != tt.want {
			t.Errorf("with version %q, DocsURL = %q, want %q", tt.version, got, tt.want)
		}
	}
}

// The script that decides what the site publishes and this package each name a build's entry. If
// they disagree, a link is dead and nothing says so: a release the script publishes as v0.1 must be
// the entry this package links, and everything the script refuses must read the development docs.
func TestDocsVersionMatchesWhatTheSitePublishes(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to run scripts/docs-version.sh")
	}
	script, err := filepath.Abs("../../scripts/docs-version.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range docsCases {
		t.Run(tt.name, func(t *testing.T) {
			links := docsVersion(tt.version) // what this package would link
			out, err := exec.Command(sh, script, "v"+tt.version).Output()
			if links == "dev" {
				if err == nil {
					t.Errorf("the site publishes v%s as %q, but docsVersion links dev", tt.version, strings.TrimSpace(string(out)))
				}
				return
			}
			if err != nil {
				t.Fatalf("docsVersion links %s for v%s, but the site publishes nothing for it (scripts/docs-version.sh: %v)", links, tt.version, err)
			}
			if got := strings.TrimSpace(string(out)); got != links {
				t.Errorf("the site publishes v%s as %q, but docsVersion links %q", tt.version, got, links)
			}
		})
	}
}
