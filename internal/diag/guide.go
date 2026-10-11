package diag

import "github.com/stuffam/drawbridge/internal/version"

// guide is the address of the Diagnostic Tools guide for the running build's version. A hint that
// can't say enough in a line sends the admin there, and never to a file in the repository, which a
// host running `doctor` doesn't have.
func guide() string {
	return version.DocsURL("guides/diagnostics/")
}
