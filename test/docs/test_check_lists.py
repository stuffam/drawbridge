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


class ExpectedDepths(unittest.TestCase):
    """What the site should show for a source page, front matter and title heading allowed for."""

    def test_a_list_in_the_front_matter_is_not_on_the_page(self):
        text = "---\ntitle: t\nhide:\n    - navigation\n---\n\n# t\n\n- a\n"
        self.assertEqual(c.expected_depths(text), [(1, 0)])

    def test_front_matter_counts_only_at_the_very_start(self):
        self.assertEqual(c.expected_depths("# t\n\n---\n- a\n---\n"), [(1, 0)])
        self.assertEqual(c.strip_front_matter("text\n---\ntitle: t\n---\n- a\n"), "text\n---\ntitle: t\n---\n- a\n")

    def test_front_matter_that_never_closes_is_left_alone(self):
        text = "---\ntitle: t\n\n- a\n"
        self.assertEqual(c.strip_front_matter(text), text)

    def test_a_page_with_no_heading_of_its_own_gets_the_themes_title_first(self):
        text = "---\ntitle: t\n---\n\n- a\n\n## h\n\n- b\n"
        self.assertEqual(c.expected_depths(text), [(1, 0), (2, 0)])

    def test_a_page_with_its_own_h1_gets_no_title_added(self):
        self.assertEqual(c.expected_depths("# t\n\n- a\n\n## h\n\n- b\n"), [(1, 0), (2, 0)])

    def test_an_h1_inside_a_code_fence_is_not_a_heading(self):
        self.assertTrue(c.adds_title("```\n# not a heading\n```\n\n## h\n"))
        self.assertFalse(c.adds_title("```\n# not a heading\n```\n\n# h\n"))

    def test_heading_text_counts_the_added_title(self):
        text = "---\ntitle: t\n---\n\nintro\n\n## First\n\n## Second\n"
        self.assertEqual(c.heading_text(text, 1), "(the page title)")
        self.assertEqual(c.heading_text(text, 2), "First")
        self.assertEqual(c.heading_text(text, 3), "Second")
        self.assertEqual(c.heading_text("# t\n\n## First\n", 2), "First")
        self.assertEqual(c.heading_text("- a\n# t\n", 0), "(before the first heading)")


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
            docs.mkdir(parents=True)
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

    def test_a_page_with_front_matter_and_no_h1_passes_when_the_site_matches(self):
        # The shape of the real pages: a title in the front matter, the theme's <h1> above the
        # article's text, and a list under a later heading.
        source = "---\ntitle: Page\nhide:\n    - navigation\n---\n\nIntro.\n\n## Steps\n\n1. a\n2. b\n"
        built = "<article><h1 id=\"__skip\">Page</h1><p>Intro.</p><h2>Steps</h2><ol><li>a</li><li>b</li></ol></article>"
        status, out = self.check(source, built)
        self.assertEqual((status, out), (0, ""))

    def test_a_flattened_list_on_a_page_with_an_added_title_names_the_right_section(self):
        source = "---\ntitle: Page\n---\n\n## Steps\n\n- a\n  - b\n"
        built = "<article><h1>Page</h1><h2>Steps</h2><ul><li>a</li><li>b</li></ul></article>"
        status, out = self.check(source, built)
        self.assertEqual(status, 1)
        self.assertIn('under "Steps"', out)

    def test_items_that_the_site_folds_into_a_paragraph_fail(self):
        status, out = self.check("# Heading\n\ntext\n- a\n- b\n", "<article><h1>t</h1><p>text - a - b</p></article>")
        self.assertEqual(status, 1)
        self.assertIn("2 list items", out)


if __name__ == "__main__":
    unittest.main()
