"""Checks that each diagram in docs/diagrams/ is what its script draws.

The README shows docs/diagrams/stack.svg, and docs/diagrams/stack.py makes it. An SVG edited by
hand, or a script changed without `make diagrams`, would have the two disagree, and the next run
of the script would quietly undo the hand edit. The script also refuses to write when a line of
text is likely to be wider than its box, which this checks too. `make docs` runs it with the other
tests in this directory.
"""

import subprocess
import sys
import tempfile
import unittest
import xml.dom.minidom
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DIAGRAMS = ROOT / "docs" / "diagrams"


class DiagramsTest(unittest.TestCase):
    def test_each_svg_is_what_its_script_draws(self):
        scripts = sorted(DIAGRAMS.glob("*.py"))
        self.assertTrue(scripts, f"no diagram scripts in {DIAGRAMS}")
        for script in scripts:
            svg = script.with_suffix(".svg")
            with self.subTest(script=script.name), tempfile.TemporaryDirectory() as tmp:
                drawn = Path(tmp) / svg.name
                run = subprocess.run(
                    [sys.executable, str(script), str(drawn)], capture_output=True, text=True
                )
                self.assertEqual(
                    run.returncode, 0, f"{script.name} failed:\n{run.stderr}{run.stdout}"
                )
                self.assertTrue(svg.exists(), f"{svg.name} isn't committed: run make diagrams")
                want = drawn.read_text(encoding="utf-8").splitlines()
                got = svg.read_text(encoding="utf-8").splitlines()
                if want != got:
                    i = next((i for i, (a, b) in enumerate(zip(want, got)) if a != b),
                             min(len(want), len(got)))
                    self.fail(
                        f"{svg.name} isn't what {script.name} draws: run make diagrams, and change "
                        f"the drawing in the script, not the SVG. Line {i + 1} first differs:\n"
                        f"  drawn:     {want[i] if i < len(want) else '(nothing)'}\n"
                        f"  committed: {got[i] if i < len(got) else '(nothing)'}"
                    )
                xml.dom.minidom.parse(str(svg))  # well-formed, or a browser shows nothing


if __name__ == "__main__":
    unittest.main()
