"""Test the release-note CLI against disposable Git repositories."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().with_name("release-notes.py")
URL = "https://github.com/example/agentnook"


class ReleaseNotesTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="nook release notes ")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                        GIT_AUTHOR_NAME="Test", GIT_AUTHOR_EMAIL="test@example.com",
                        GIT_COMMITTER_NAME="Test", GIT_COMMITTER_EMAIL="test@example.com")
        self.git("init", "-q", "-b", "main")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.root, env=self.env,
                              check=True, capture_output=True, text=True).stdout.strip()

    def commit(self, subject, body=None):
        args = ["commit", "-q", "--allow-empty", "-m", subject]
        if body is not None:
            args.extend(["-m", body])
        self.git(*args)
        return self.git("rev-parse", "HEAD")

    def notes(self, tag, repo=None):
        return subprocess.run([sys.executable, str(SCRIPT), tag, "--repository-url", URL],
                              cwd=repo or self.root, env=self.env, capture_output=True, text=True)

    def test_grouped_notes_from_previous_reachable_stable_tag(self):
        self.commit("feat: already released")
        self.git("tag", "-a", "v1.9.0", "-m", "Release 1.9.0")
        self.git("switch", "-c", "other")
        self.commit("feat: unreachable branch")
        self.git("tag", "v1.10.0")
        self.git("switch", "main")
        feature = self.commit("feat(network): show HTTP host")
        self.git("tag", "v1.11.0-rc.1")
        self.commit("fix: escape [link] <img> @nobody")
        self.commit("refactor!: replace config format")
        self.commit("feat(cli): rename command", "Details.\n\nBREAKING CHANGE: update your aliases")
        self.commit("docs: update guide")
        self.commit("meta: clean up examples")
        self.commit("Update examples without a prefix")
        self.git("tag", "v1.11.0")
        self.commit("feat: not in this tag")

        result = self.notes("v1.11.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        notes = result.stdout
        sections = [line for line in notes.splitlines() if line.startswith("## ")]
        self.assertEqual(sections, ["## Breaking changes", "## Features", "## Bug fixes",
                                    "## Documentation", "## Other changes"])
        self.assertIn(f"**network:** show HTTP host ([`{feature[:12]}`]({URL}/commit/{feature}))", notes)
        self.assertIn(r"escape \[link\] &lt;img&gt; &#64;nobody", notes)
        self.assertIn("replace config format", notes)
        self.assertIn("  - update your aliases", notes)
        self.assertIn(f"{URL}/compare/v1.9.0...v1.11.0", notes)
        for omitted in ("already released", "unreachable branch", "without a prefix", "not in this tag", "## Refactoring"):
            self.assertNotIn(omitted, notes)

    def test_prerelease_uses_stable_baseline(self):
        self.commit("feat: first release")
        self.git("tag", "v1.2.0")
        self.commit("fix: later stable patch")
        self.git("tag", "v1.2.10")
        self.commit("feat: first candidate")
        self.git("tag", "v1.3.0-rc.1")
        self.commit("fix: second candidate")
        self.git("tag", "v1.3.0-rc.2")
        self.git("tag", "v1.3.0")  # Same commit: never a base for its prerelease.

        result = self.notes("v1.3.0-rc.2")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("first candidate", result.stdout)
        self.assertIn("second candidate", result.stdout)
        self.assertNotIn("later stable patch", result.stdout)
        self.assertIn("v1.2.10...v1.3.0-rc.2", result.stdout)

    def test_first_release_and_empty_range(self):
        self.commit("feat: initial feature")
        self.git("tag", "v0.1.0")
        self.git("tag", "v0.1.1")
        first = self.notes("v0.1.0")
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertIn("initial feature", first.stdout)
        self.assertNotIn("Full changelog", first.stdout)
        empty = self.notes("v0.1.1")
        self.assertEqual(empty.returncode, 0, empty.stderr)
        self.assertIn("No Conventional Commits", empty.stdout)
        self.assertIn("v0.1.0...v0.1.1", empty.stdout)

    def test_invalid_missing_and_shallow_tags_fail(self):
        self.commit("feat: initial feature")
        self.git("tag", "v0.1.0")
        for tag in ("HEAD", "v0.2.0"):
            with self.subTest(tag=tag):
                result = self.notes(tag)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
        self.git("clone", "-q", "--depth", "1", self.root.as_uri(), "shallow")
        result = self.notes("v0.1.0", self.root / "shallow")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("require full Git history and tags", result.stderr)


if __name__ == "__main__":
    unittest.main()
