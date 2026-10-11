package version

import "regexp"

// docsSite is where the documentation is published: one entry for each minor release, and `dev`
// for the development docs (docs/adr/0013-docs-site-zensical.md).
const docsSite = "https://stuffam.github.io/drawbridge/"

// finalVersion is a release: three numbers and nothing after them. It is the pattern of
// scripts/docs-version.sh, without the v the constant doesn't have, because the site publishes
// docs for exactly the versions that script accepts. TestDocsVersionMatchesWhatTheSitePublishes
// runs the two against each other.
var finalVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// DocsURL is the address of a page of the documentation that matches this build, such as
// "guides/backup-restore/". A release reads the docs of its minor version, so what an admin reads
// is what they installed; anything else (a pre-release, or a build made between releases) has no
// docs of its own and reads the development docs.
func DocsURL(page string) string {
	return docsSite + docsVersion(Version) + "/" + page
}

// docsVersion is the site's entry for a version: v0.1 for 0.1.3, and dev for everything the site
// doesn't publish.
func docsVersion(ver string) string {
	m := finalVersion.FindStringSubmatch(ver)
	if m == nil {
		return "dev"
	}
	return "v" + m[1] + "." + m[2]
}
