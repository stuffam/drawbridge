"""Tests for check_lists.py: `make docs` runs them with `python -m unittest discover -s test/docs`."""

import io
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

import check_lists as c


def site_page(*items):
    """A built page: (section, depth) pairs as nested lists, all in one heading's section."""
    html, depth = ["<article><h1>t</h1>"], -1
    for _, d in items:
        while depth > d:
            html.append("</li></ul>")
            depth -= 1
        if depth == d:
            html.append("</li>")
        while depth < d:
            html.append("<ul>")
            depth += 1
        html.append("<li>x")
    html.append("</li></ul>" * (depth + 1) + "</article>")
    return "".join(html)


class SourceDepths(unittest.TestCase):
    def test_two_spaces_nest_on_github(self):
        self.assertEqual(c.source_depths("- a\n  - b\n    - c\n- d\n"), [(0, 0), (0, 1), (0, 2), (0, 0)])

    def test_four_spaces_nest_too(self):
        self.assertEqual(c.source_depths("- a\n    - b\n"), [(0, 0), (0, 1)])

    def test_nested_under_a_numbered_item_needs_its_content_offset(self):
        self.assertEqual(c.source_depths("1. a\n   - b\n"), [(0, 0), (0, 1)])
        self.assertEqual(c.source_depths("1. a\n  - b\n"), [(0, 0), (0, 0)])

    def test_a_list_after_a_paragraph_is_a_list_but_a_late_number_is_not(self):
        self.assertEqual(c.source_depths("text\n- a\n"), [(0, 0)])
        self.assertEqual(c.source_depths("text\n1. a\n"), [(0, 0)])
        self.assertEqual(c.source_depths("text\n2. a\n"), [])

    def test_code_is_not_a_list(self):
        self.assertEqual(c.source_depths("```\n- a\n```\n- b\n"), [(0, 0)])

    def test_headings_count_sections_and_end_lists(self):
        self.assertEqual(c.source_depths("- a\n# h\n  - b\n"), [(0, 0), (1, 0)])


class BuiltDepths(unittest.TestCase):
    def depths(self, html):
        parser = c.Lists()
        parser.feed(html)
        return parser.depths

    def test_reads_nesting_and_sections(self):
        html = (
            "<article><h1>t</h1><ul><li>a<ul><li>b</li></ul></li></ul>"
            "<h2>u</h2><ul><li>c</li></ul></article>"
        )
        self.assertEqual(self.depths(html), [(1, 0), (1, 1), (2, 0)])

    def test_ignores_what_is_outside_the_article(self):
        self.assertEqual(self.depths("<nav><ul><li>menu</li></ul></nav><article></article>"), [])


class Pages(unittest.TestCase):
    """The checker against a source page and a built one, as `make docs` runs it."""

    def check(self, source, built):
        with tempfile.TemporaryDirectory() as tmp:
            docs, site = Path(tmp, "docs/user_docs"), Path(tmp, "site")
            (site / "page").mkdir(parents=True)
            docs.mkdir()
            (docs / "page.md").write_text(source)
            (site / "page" / "index.html").write_text(built)
            out, err = io.StringIO(), io.StringIO()
            with redirect_stdout(out), redirect_stderr(err):
                status = c.main([str(docs), str(site)])
            return status, out.getvalue()

    def test_a_page_that_shows_what_github_shows_passes(self):
        status, _ = self.check("# t\n\n- a\n    - b\n", site_page((1, 0), (1, 1)))
        self.assertEqual(status, 0)

    def test_nested_bullets_that_the_site_flattens_fail_and_name_the_section(self):
        status, out = self.check("# Heading\n\n- a\n  - b\n", site_page((1, 0), (1, 0)))
        self.assertEqual(status, 1)
        self.assertIn('under "Heading"', out)

    def test_items_that_the_site_folds_into_a_paragraph_fail(self):
        status, out = self.check("# Heading\n\ntext\n- a\n- b\n", "<article><h1>t</h1><p>text - a - b</p></article>")
        self.assertEqual(status, 1)
        self.assertIn("2 list items", out)


if __name__ == "__main__":
    unittest.main()
