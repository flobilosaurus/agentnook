"""No real Herdr session, credentials or VMs needed."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("testing_session", Path(__file__).with_name("testing-session.py"))
session = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(session)


class SessionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="nook testing ")
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name).resolve() / ".agentnook"
        self.root = self.home / "testing-session"
        self.root.mkdir(parents=True)
        self.active = self.root / "active.json"
        self.nook = self.home / "nook"
        self.nook.write_text("#!/usr/bin/env python3\nimport time\ntime.sleep(120)\n")
        self.nook.chmod(0o700)
        (self.home / "config.toml").write_text('[default_images]\npi = "pi-test"\n')
        self.args = argparse.Namespace(nook=str(self.nook), network="ask")
        self.children = []
        self.panes = set()
        self.calls = []
        self.addCleanup(self.reap)
        self.env = patch.dict(os.environ, HERDR_ENV="1", HERDR_SOCKET_PATH="/test/herdr.sock",
                              HERDR_WORKSPACE_ID="w1", HERDR_PANE_ID="w1:p1")
        self.env.start()
        self.addCleanup(self.env.stop)

    def reap(self):
        for child in self.children:
            if child.poll() is None:
                child.terminate()
            child.wait(timeout=10)

    def herdr(self, *args):
        self.calls.append(args)
        if args[0] == "split":
            pane = f"w1:p{len(self.panes) + 2}"
            self.panes.add(pane)
            return {"result": {"pane": {"pane_id": pane}}}
        if args[0] == "run":
            self.children.append(subprocess.Popen(shlex.split(args[2]), start_new_session=True,
                                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
        if args[0] == "close":
            self.assertNotEqual(args[1], "w1:p1")
            self.panes.remove(args[1])
        return {"result": {}}

    def desktop(self, home, action=None, **args):
        self.calls.append((action, args))
        if action is None:
            return {"spaces": [
                {"machine": "remote", "tabs": [{"terminals": [{"terminal": "t1", "pane": "remote"}]}]},
                {"tabs": [{"terminals": [
                    {"terminal": "t1", "pane": "origin"},
                    *({"terminal": f"t-{p}", "pane": p} for p in sorted(self.panes)),
                ]}]},
            ]}
        if action == "pane.split":
            self.assertIn(args["pane"], self.panes | {"origin"})
            self.assertFalse((Path(args["argv"][-2]) / "start").exists())
            pane = f"p{len(self.panes) + 2}"
            self.panes.add(pane)
            self.children.append(subprocess.Popen(args["argv"], start_new_session=True,
                                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
            return {"pane": pane, "terminal": f"t-{pane}"}
        if action == "pane.close":
            self.panes.remove(args["pane"])
        return {}

    def start(self):
        # Only tool discovery is stubbed; workers execute real child processes.
        with patch.object(session, "herdr", self.herdr), patch.object(session.shutil, "which", side_effect=lambda x: x):
            session.up(self.args, self.home, self.root, self.active)
        return json.loads(self.active.read_text())

    def test_up_down_and_layout(self):
        state = self.start()
        splits = [c for c in self.calls if c[0] == "split"]
        expected = {"private-1", "private-2", "work-1", "work-2", "watch"}
        self.assertEqual(set(state["panes"]), expected)
        self.assertEqual(set(state["launched"]), expected)
        self.assertEqual(len(splits), 5)
        self.assertIn("--no-focus", splits[0])
        self.assertEqual(splits[0][2:5], ("w1:p1", "--direction", "down"))
        self.assertEqual(splits[1][2:5], (state["panes"]["private-1"], "--direction", "right"))
        self.assertEqual(len(self.panes), 5)
        self.assertEqual(len(set(state["projects"].values())), 4)
        for path in map(Path, state["projects"].values()):
            self.assertEqual(path.parent, self.home.parent / ".agentnook-testing-projects")
            self.assertFalse(path.is_relative_to(self.home))
            self.assertFalse(self.home.is_relative_to(path))
        self.assertEqual(session.command(state, "watch"), [state["nook"], "watch"])
        for role in session.VM_ROLES:
            cmd = session.command(state, role)
            self.assertIn(role.rsplit("-", 1)[0], cmd)
            self.assertIn("--clear-allow-private", cmd)
        with patch.object(session, "herdr", self.herdr):
            session.down(state, Path(state["directory"]), self.active, 10)
        self.assertFalse(self.active.exists())
        self.assertFalse(self.panes)
        self.assertTrue((self.home / "config.toml").exists())
        self.assertTrue(all(Path(p).exists() for p in state["projects"].values()))

    def test_desktop_up_down_and_layout(self):
        with patch.dict(os.environ, HERDR_ENV="", HC_TERMINAL_ID="t1", HERDR_APP_HOME=str(self.home)), \
                patch.object(session, "desktop", self.desktop), \
                patch.object(session, "herdr") as tui, \
                patch.object(session.shutil, "which", side_effect=lambda x: x):
            session.up(self.args, self.home, self.root, self.active)
            state = json.loads(self.active.read_text())
            self.assertEqual(state["app_home"], str(self.home))
            self.assertEqual(state["origin"], "origin")
            self.assertEqual(set(state["panes"]), {"private-1", "private-2", "work-1", "work-2", "watch"})
            splits = [args for action, args in self.calls if action == "pane.split"]
            self.assertEqual(len(splits), 5)
            self.assertEqual(splits[0]["pane"], "origin")
            self.assertEqual(splits[0]["direction"], "down")
            self.assertEqual(splits[1]["pane"], state["panes"]["private-1"])
            self.assertEqual(splits[1]["direction"], "right")
            self.assertEqual(splits[1]["argv"][-1], "watch")
            # A pane already removed from the window must not be closed again.
            self.panes.remove(state["panes"]["watch"])
            session.down(state, Path(state["directory"]), self.active, 10)
            self.assertFalse(self.panes)
            self.assertFalse(self.active.exists())
            tui.assert_not_called()

    def test_desktop_interrupted_layout_never_releases_workers(self):
        def fail_second_split(home, action=None, **args):
            if action == "pane.split" and self.panes:
                raise RuntimeError("split failed")
            return self.desktop(home, action, **args)

        with patch.dict(os.environ, HERDR_ENV="", HC_TERMINAL_ID="t1", HERDR_APP_HOME=str(self.home)), \
                patch.object(session, "desktop", fail_second_split), \
                patch.object(session.shutil, "which", side_effect=lambda x: x):
            with self.assertRaisesRegex(RuntimeError, "split failed"):
                session.up(self.args, self.home, self.root, self.active)
            state = json.loads(self.active.read_text())
            directory = Path(state["directory"])
            self.assertFalse((directory / "start").exists())
            self.children[0].wait(timeout=5)
            self.assertFalse((directory / "private-1.started").exists())
            session.down(state, directory, self.active, 0)
            self.assertFalse(self.panes)
            self.assertFalse(self.active.exists())

    def test_desktop_rejects_terminal_without_visible_pane(self):
        with patch.dict(os.environ, HERDR_ENV="", HC_TERMINAL_ID="unknown"), \
                patch.object(session, "desktop", self.desktop):
            with self.assertRaisesRegex(RuntimeError, "no local Desktop pane"):
                session.target()

    def test_duplicate_up_rejected(self):
        self.active.write_text("{}")
        with self.assertRaisesRegex(RuntimeError, "already recorded"):
            session.up(self.args, self.home, self.root, self.active)

    def test_worker_queued_after_stop_never_starts(self):
        directory = self.root / "late"
        directory.mkdir()
        session.save(directory / "session.json", {})
        (directory / "stop").touch()
        with patch.object(session.subprocess, "Popen") as spawn, patch.object(session.signal, "signal"):
            session.worker(directory, "private-1")
        spawn.assert_not_called()
        self.assertTrue((directory / "private-1.done").exists())

    def test_timeout_retains_state_and_panels(self):
        directory = self.root / "stalled"
        directory.mkdir()
        state = {"socket": "/test/herdr.sock", "launched": ["private-1"]}
        session.save(self.active, state)
        with patch.object(session, "herdr") as control:
            with self.assertRaisesRegex(RuntimeError, "Cleanup still pending"):
                session.down(state, directory, self.active, 0)
        control.assert_not_called()
        self.assertTrue(self.active.exists())

    def test_wrong_server_rejected(self):
        with self.assertRaisesRegex(RuntimeError, "original Herdr"):
            session.same_server({"socket": "/other/server.sock"})

    def test_symlinked_projects_cannot_overlap_data_home(self):
        (self.home.parent / ".agentnook-testing-projects").symlink_to(self.home, target_is_directory=True)
        with patch.object(session, "herdr") as control, patch.object(session.shutil, "which", side_effect=lambda x: x):
            with self.assertRaisesRegex(RuntimeError, "must not overlap"):
                session.up(self.args, self.home, self.root, self.active)
        control.assert_not_called()
        self.assertFalse(self.active.exists())

    def test_missing_image_fails_before_panels(self):
        (self.home / "config.toml").unlink()
        with patch.object(session, "herdr") as control, patch.object(session.shutil, "which", side_effect=lambda x: x):
            with self.assertRaisesRegex(RuntimeError, "Pi default image required"):
                session.up(self.args, self.home, self.root, self.active)
        control.assert_not_called()
        self.assertFalse(self.active.exists())


if __name__ == "__main__":
    unittest.main()
