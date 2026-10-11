package diag

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stuffam/drawbridge/internal/version"
)

// A hint that sends the admin to read something names a guide on the docs site for the running
// build's version, so what's read matches what's installed (internal/version.DocsURL).
func TestHintsLinkTheGuideOfThisBuild(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })

	f := healthy()
	f.snap.Uplinks6 = nil // the uplink warns, and its hint names the guide
	for _, tt := range []struct{ version, want string }{
		{"0.1.3", "https://stuffam.github.io/drawbridge/v0.1/guides/diagnostics/"},
		{"0.1.0-rc.2+67", "https://stuffam.github.io/drawbridge/dev/guides/diagnostics/"},
	} {
		version.Version = tt.version
		if hint := f.check(t, "uplink").Hint; !strings.Contains(hint, tt.want) {
			t.Errorf("with version %q, the hint %q doesn't name %s", tt.version, hint, tt.want)
		}
	}
}

// The package ships no docs/, and a host that runs `doctor` has no checkout, so a string the
// admin might read never names a file there: `docs/REQUIREMENTS.md` stood in three hints until
// they pointed at the guide.
func TestNoStringNamesARepositoryFile(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "docs/") {
				t.Errorf("%s: %s names a path under docs/; point at a guide with version.DocsURL instead", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no Go files were read, so the check proves nothing")
	}
}
