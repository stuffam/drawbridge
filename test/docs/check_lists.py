#!/usr/bin/env python3
"""Checks that the docs site shows every list the way GitHub does.

The docs are written as GitHub-flavored Markdown, and Zensical's parser (Python-Markdown) reads
two things differently: a nested list must be indented 4 spaces, not 2, and a list must follow a
blank line. A page that breaks either rule still builds, with its nested bullets flattened or
folded into the paragraph above, so `zensical build --strict` can't see it.

This reads each page's source the way GitHub (CommonMark) does, notes the depth of every list
item, and compares that with the depth of every <li> in the built page. Standard library only.

    check_lists.py [--source-only] DOCS_DIR [SITE_DIR]

Exit status 1 when any page differs (and 2 on a bad call). With --source-only, prints each page's
depths (one number per item) and doesn't need a built site; that is how to see what a change did.
"""

import re
import sys
from html.parser import HTMLParser
from pathlib import Path

ITEM = re.compile(r"^( *)([-*+]|(\d{1,9})[.)])( +|$)")
FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})")


HEADING = re.compile(r"^ {0,3}#{1,6}( |$)")


def source_depths(text):
    """(section, depth) of each list item in a page, in order: the section is the number of
    headings above the item, and the depth is 0 for a top-level list."""
    depths = []
    section = 0
    stack = []  # (content offset) of each open item
    fence = None
    blank_before = True
    for line in text.split("\n"):
        m = FENCE.match(line)
        if fence:
            if m and m.group(1)[0] == fence[0] and len(m.group(1)) >= len(fence):
                fence = None
            continue
        if m:
            fence = m.group(1)
            blank_before = False
            continue
        if not line.strip():
            blank_before = True
            continue
        indent = len(line) - len(line.lstrip(" "))
        if indent < 4 and HEADING.match(line):
            section += 1
            stack.clear()
            blank_before = False
            continue
        item = ITEM.match(line)
        # An ordered item can interrupt a paragraph only when it starts at 1.
        if item and not blank_before and item.group(3) not in (None, "1") and not stack:
            item = None
        if item and stack and indent >= stack[-1] + 4 and blank_before:
            item = None  # an indented code block, not an item
        if item:
            while stack and indent < stack[-1]:
                stack.pop()
            depths.append((section, len(stack)))
            spaces = len(item.group(4))
            stack.append(indent + len(item.group(2)) + (spaces if 1 <= spaces <= 4 else 1))
        elif blank_before:
            # A paragraph after a blank line belongs to the innermost item it's indented under.
            while stack and indent < stack[-1]:
                stack.pop()
        blank_before = False
    return depths


class Lists(HTMLParser):
    """(section, depth) of each <li> inside the page's <article>, as source_depths gives them."""

    def __init__(self):
        super().__init__()
        self.depths = []
        self.depth = -1
        self.section = 0
        self.in_article = False

    def handle_starttag(self, tag, attrs):
        if tag == "article":
            self.in_article = True
        elif self.in_article and re.fullmatch(r"h[1-6]", tag):
            self.section += 1
        elif self.in_article and tag in ("ul", "ol"):
            self.depth += 1
        elif self.in_article and tag == "li":
            self.depths.append((self.section, self.depth))

    def handle_endtag(self, tag):
        if self.in_article and tag in ("ul", "ol"):
            self.depth -= 1
        elif tag == "article":
            self.in_article = False


def built_page(site, docs, page):
    rel = page.relative_to(docs).with_suffix("")
    if rel.name == "index":
        return site / rel.parent / "index.html"
    if rel.name == "README":
        return site / rel.parent / "index.html"
    return site / rel / "index.html"


def first_difference(want, got):
    """The first section whose items differ, with what each side has there."""
    for section in sorted({s for s, _ in want} | {s for s, _ in got}):
        a = [d for s, d in want if s == section]
        b = [d for s, d in got if s == section]
        if a != b:
            return section, a, b
    return None


def heading_text(text, section):
    """The text of the section-th heading, for the message."""
    n = 0
    fence = False
    for line in text.split("\n"):
        if FENCE.match(line):
            fence = not fence
        elif not fence and HEADING.match(line):
            n += 1
            if n == section:
                return line.lstrip("# ").strip()
    return "(before the first heading)"


def main(argv):
    source_only = "--source-only" in argv
    args = [a for a in argv if a != "--source-only"]
    if len(args) != (1 if source_only else 2):
        print(__doc__, file=sys.stderr)
        return 2
    docs = Path(args[0])
    failed = 0
    for page in sorted(docs.rglob("*.md")):
        source = page.read_text(encoding="utf-8")
        want = source_depths(source)
        if source_only:
            print(page.relative_to(docs), "".join(f"{s}:{d} " for s, d in want))
            continue
        parser = Lists()
        parser.feed(built_page(Path(args[1]), docs, page).read_text(encoding="utf-8"))
        got = parser.depths
        if want != got:
            failed += 1
            section, a, b = first_difference(want, got)
            print(
                f"{page}: under \"{heading_text(source, section)}\", GitHub shows {len(a)} list "
                f"items (depths {''.join(map(str, a))}) and the site {len(b)} "
                f"(depths {''.join(map(str, b))}); {len(want)} items in all, {len(got)} on the site"
            )
    if failed:
        print(
            f"{failed} page(s) show their lists differently from GitHub: indent nested lists "
            "4 spaces, and put a blank line before every list",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
