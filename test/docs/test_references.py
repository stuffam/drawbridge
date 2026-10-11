"""Checks that every `docs/...md` path the repository names is a file that exists.

The user docs moved from docs/ to docs/user_docs/, and three links in the web UI went dead (they
pointed at github.com/.../blob/main/docs/api-tokens.md) with nothing to say so. This reads every
tracked text file for a path like that, in prose, in code, or inside a URL, and fails on one that
names no file. A second check keeps the package's maintainer scripts from printing such a path to an
admin at all. `make docs` runs it with the other tests in this directory.
"""

import re
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# A path such as docs/PLAN.md or docs/user_docs/guides/two-factor.md. The lookbehind keeps
# user_docs/guides/x.md from being read as docs/guides/x.md.
PATH = re.compile(r"(?<![\w])docs/[\w./-]*?\.md")

# The changelog quotes old release notes, whose links are fixed for good (docs/releasing.md, step 2),
# and this file names dead paths on purpose, as examples of what it looks for.
SKIP = {"CHANGELOG.md", Path(__file__).resolve().relative_to(ROOT).as_posix()}

# What the package's scripts say to an admin while dpkg runs. An admin has no checkout, so a message
# that sends them to read something names a guide on the docs site, never a file under docs/.
MAINTAINER_SCRIPTS = ["packaging/deb/postinst", "packaging/deb/prerm", "packaging/deb/postrm"]
GUIDE_SITE = "https://stuffam.github.io/drawbridge/"


def tracked_files():
    out = subprocess.run(
        ["git", "ls-files", "-z"], cwd=ROOT, capture_output=True, check=True
    ).stdout.decode()
    return [p for p in out.split("\0") if p]


def references():
    """(file, line number, path) for each docs/...md path in a tracked text file."""
    for name in tracked_files():
        if name in SKIP or name.startswith(".claude/"):
            continue
        path = ROOT / name
        if not path.is_file():
            continue
        try:
            text = path.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError):
            continue  # not text
        for number, line in enumerate(text.split("\n"), 1):
            for m in PATH.finditer(line):
                yield name, number, m.group(0)


class References(unittest.TestCase):
    def test_every_named_doc_exists(self):
        dead = [f"{f}:{n}: {p}" for f, n, p in references() if not (ROOT / p).is_file()]
        self.assertEqual(dead, [], "these name a file under docs/ that doesn't exist")

    def test_maintainer_scripts_print_no_repository_path(self):
        printed = []
        for name in MAINTAINER_SCRIPTS:
            for number, line in enumerate((ROOT / name).read_text(encoding="utf-8").split("\n"), 1):
                if re.search(r"\becho\b.*docs/", line):
                    printed.append(f"{name}:{number}: {line.strip()}")
        self.assertEqual(
            printed,
            [],
            "an admin has no checkout: send them to a guide on " + GUIDE_SITE + " instead",
        )

    def test_postinst_sends_a_lost_ipv6_to_the_diagnostics_guide(self):
        text = (ROOT / "packaging/deb/postinst").read_text(encoding="utf-8")
        self.assertIn(GUIDE_SITE + "latest/guides/diagnostics/", text)

    def test_the_check_reads_urls_and_ignores_user_docs_paths(self):
        found = PATH.findall(
            "https://github.com/x/y/blob/main/docs/api-tokens.md and (docs/PLAN.md) "
            "and docs/user_docs/guides/two-factor.md"
        )
        self.assertEqual(
            found, ["docs/api-tokens.md", "docs/PLAN.md", "docs/user_docs/guides/two-factor.md"]
        )
        self.assertEqual(PATH.findall("see user_docs/guides/two-factor.md"), [])


if __name__ == "__main__":
    unittest.main()
