#!/usr/bin/env python3
"""Manage four Pi VMs and one Watch pane in the calling Herdr tab (Python 3.11+)."""

import argparse
import fcntl
import json
import os
from pathlib import Path
import pwd
import shlex
import shutil
import signal
import subprocess
import sys
import threading
import time
import tomllib
import uuid

VM_ROLES = ("private-1", "private-2", "work-1", "work-2")
ROLES = (*VM_ROLES, "watch")
SCRIPT = Path(__file__).resolve()


def save(path, value):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(path)


def run_json(argv, env=None):
    result = subprocess.run(argv, capture_output=True, text=True, env=env)
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or result.stdout.strip())
    return json.loads(result.stdout)


def herdr(*args):
    return run_json(["herdr", "pane", *args])


def desktop_home():
    # Agentenv profiles can override HOME while the GUI uses the real user's home.
    path = os.environ.get("HERDR_APP_HOME") or str(
        Path(pwd.getpwuid(os.getuid()).pw_dir) / ".config" / "herdr-app")
    return str(Path(path).expanduser().resolve())


def desktop(home, action=None, **args):
    env = dict(os.environ, HERDR_APP_HOME=home)
    argv = ["herdr-app", "do", action, json.dumps(args)] if action else ["herdr-app", "ls"]
    try:
        return run_json(argv, env)
    except OSError as error:
        raise RuntimeError("Herdr Desktop requires herdr-app in PATH.") from error


def desktop_panes(home):
    return {entry["pane"]: entry.get("terminal")
            for space in desktop(home)["spaces"] if not space.get("machine")
            for tab in space["tabs"] for entry in tab["terminals"] if entry.get("pane")}


def target():
    if os.environ.get("HERDR_ENV") == "1" and all(os.environ.get(k) for k in
            ("HERDR_SOCKET_PATH", "HERDR_PANE_ID", "HERDR_WORKSPACE_ID")):
        return dict(socket=os.environ["HERDR_SOCKET_PATH"], workspace=os.environ["HERDR_WORKSPACE_ID"],
                    origin=os.environ["HERDR_PANE_ID"])
    terminal = os.environ.get("HC_TERMINAL_ID")
    if terminal:
        home = desktop_home()
        for pane, address in desktop_panes(home).items():
            if address == terminal:
                return dict(app_home=home, origin=pane, terminals={}, worker_env={
                    key: os.environ[key] for key in ("HOME", "PATH", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
                                                     "XDG_STATE_HOME", "SMOLVM_HOME") if key in os.environ})
        raise RuntimeError("Current herdr-core terminal has no local Desktop pane. Open it in Herdr Desktop first.")
    raise RuntimeError("Run up in a terminal shown in Herdr Desktop or in the target Herdr TUI tab.")


def worker_argv(directory, role):
    return [sys.executable, str(SCRIPT), "_worker", str(directory), role]


def command(state, role):
    if role == "watch":
        return [state["nook"], "watch"]
    return [state["nook"], "--agent", "pi", "--profile", role.rsplit("-", 1)[0],
            "--network", state["network"], "--clear-allow-private",
            "--cpus", "2", "--mem", "2048"]


def worker(directory, role):
    """Own child process directly; never signal saved/recycled PIDs."""
    state = json.loads((directory / "session.json").read_text())
    stop = threading.Event()
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, lambda *_: stop.set())
    child = None
    code = 1
    try:
        # Desktop starts the worker when it creates the split. Wait until all
        # panes are recorded before allowing any worker to start a VM.
        while state.get("app_home") and not (directory / "start").exists():
            if stop.wait(0.2) or (directory / "stop").exists():
                code = 0
                return
        # A queued Herdr command may arrive after down has already been requested.
        if stop.is_set() or (directory / "stop").exists():
            code = 0
            return
        env = {**os.environ, **state.get("worker_env", {}), "AGENTNOOK_HOME": state["home"]}
        cwd = state["projects"].get(role, state["home"])
        child = subprocess.Popen(command(state, role), cwd=cwd, env=env)
        (directory / (role + ".started")).touch()
        while child.poll() is None:
            if stop.wait(0.2) or (directory / "stop").exists():
                child.terminate()  # nook handles SIGTERM and removes its VM.
                child.wait()  # Do not SIGKILL: preserve VM/history cleanup.
                break
        code = child.returncode
    finally:
        if child is not None and child.poll() is None:
            child.terminate()
            child.wait()
        save(directory / (role + ".done"), {"exit_code": code})


def split(state, directory, state_file, target, direction, role, ratio=0.5):
    if app_home := state.get("app_home"):
        result = desktop(app_home, "pane.split", pane=target, direction=direction,
                         argv=worker_argv(directory, role))
        pane, terminal = result.get("pane"), result.get("terminal")
        if not all(isinstance(value, str) and value for value in (pane, terminal)):
            raise RuntimeError("Desktop split did not return pane and terminal IDs. Check the window.")
        state["terminals"][role] = terminal
        state["launched"].append(role)
    else:
        result = herdr("split", "--pane", target, "--direction", direction,
                       "--ratio", str(ratio), "--cwd", state["home"], "--no-focus")
        pane = result["result"]["pane"]["pane_id"]
    state["panes"][role] = pane
    save(state_file, state)
    if app_home:
        desktop(app_home, "pane.rename", pane=pane, name="nook " + role)
    else:
        herdr("rename", pane, "nook " + role)
    return pane


def same_server(state):
    if app_home := state.get("app_home"):
        if app_home != desktop_home():
            raise RuntimeError("Run this command with the original HERDR_APP_HOME.")
    elif state["socket"] != os.environ.get("HERDR_SOCKET_PATH"):
        raise RuntimeError("Run this command in the original Herdr server/session.")


def down(state, directory, state_file, timeout):
    same_server(state)
    (directory / "stop").touch()
    app_home = state.get("app_home")
    launched = state["launched"] if not app_home or (directory / "start").exists() else []
    deadline = time.monotonic() + timeout
    while True:
        pending = [r for r in launched if not (directory / (r + ".done")).exists()]
        if not pending:
            break
        if time.monotonic() >= deadline:
            raise RuntimeError("Cleanup still pending: " + ", ".join(pending) +
                               ". Inspect panels, then retry down; state retained.")
        time.sleep(0.2)
    # Inspect before closing: a user may already have closed an owned pane.
    failures = []
    visible = desktop_panes(app_home) if app_home else {}
    for role, pane in reversed(list(state["panes"].items())):
        if app_home:
            # The user might have closed the pane or replaced its terminal.
            if visible.get(pane) == state["terminals"][role]:
                try:
                    desktop(app_home, "pane.close", pane=pane)
                except RuntimeError as error:
                    failures.append(f"{pane}: {error}")
            continue
        try:
            herdr("get", pane)
        except RuntimeError as error:
            # Do not mistake transport/server failures for absent panes.
            if "not_found" in str(error) or "not found" in str(error).lower():
                continue
            failures.append(f"{pane}: {error}")
            continue
        try:
            herdr("close", pane)
        except RuntimeError as error:
            failures.append(f"{pane}: {error}")
    if failures:
        raise RuntimeError("Panel cleanup incomplete; retry down:\n" + "\n".join(failures))
    state_file.unlink()
    print("Testsession stopped. Profiles, logins, test folders and history retained.")


def up(args, home, root, state_file):
    if state_file.exists():
        raise RuntimeError("Session already recorded. Use status or down first.")
    destination = target()
    nook = shutil.which(args.nook) if args.nook else None
    if not nook:
        local = SCRIPT.parent.parent / "bin" / "nook"
        nook = shutil.which("nook") or (str(local) if local.is_file() else None)
    if not nook:
        raise RuntimeError("nook missing; run mise run build or pass --nook PATH.")
    if not shutil.which("smolvm"):
        raise RuntimeError("smolvm >= 1.36.1 required in PATH.")
    # Require an existing image: four concurrent first-time builds are unsafe.
    config = home / "config.toml"
    if not config.exists() or not tomllib.loads(config.read_text()).get("default_images", {}).get("pi"):
        raise RuntimeError(f"Pi default image required. Run AGENTNOOK_HOME={shlex.quote(str(home))} "
                           f"{shlex.quote(nook)} images build pi-testing --agent pi --set-default first.")
    # nook refuses to mount a project that overlaps its host data directory.
    project_root = (home.parent / (home.name + "-testing-projects")).resolve()
    projects = {}
    for role in VM_ROLES:
        project = (project_root / role).resolve()
        if project.is_relative_to(home) or home.is_relative_to(project):
            raise RuntimeError(f"Test project and AGENTNOOK_HOME must not overlap: {project} and {home}")
        project.mkdir(parents=True, exist_ok=True, mode=0o700)
        projects[role] = str(project)
    directory = root / uuid.uuid4().hex
    directory.mkdir(mode=0o700)
    state = dict(directory=str(directory), home=str(home), nook=str(Path(nook).resolve()),
                 network=args.network, projects=projects, panes={}, launched=[], **destination)
    save(state_file, state)
    save(directory / "session.json", state)
    try:
        # Keep the control pane above a 2×2 VM grid with Watch on the right.
        private1 = split(state, directory, state_file, state["origin"], "down", "private-1", 0.8)
        split(state, directory, state_file, private1, "right", "watch")
        work1 = split(state, directory, state_file, private1, "down", "work-1")
        split(state, directory, state_file, private1, "right", "private-2")
        split(state, directory, state_file, work1, "right", "work-2")
        if state.get("app_home"):
            (directory / "start").touch()
        else:
            for role in ROLES:
                # Persist before queuing, so ambiguous command failures remain recoverable.
                state["launched"].append(role)
                save(state_file, state)
                herdr("run", state["panes"][role], shlex.join(worker_argv(directory, role)))
        deadline = time.monotonic() + 20
        while not all((directory / (r + ".started")).exists() for r in ROLES):
            failed = [r for r in ROLES if (directory / (r + ".done")).exists()]
            if failed or time.monotonic() > deadline:
                raise RuntimeError("Panel startup failed/timed out: " + ", ".join(failed))
            time.sleep(0.2)
        # Popen success only proves the executable was spawned, not that nook
        # passed its startup checks. Catch immediate exits before reporting launch.
        deadline = time.monotonic() + 1
        while True:
            failed = [r for r in ROLES if (directory / (r + ".done")).exists()]
            if failed:
                raise RuntimeError("Processes exited during startup (see panels): " + ", ".join(failed))
            if time.monotonic() >= deadline:
                break
            time.sleep(0.2)
        print("Panel processes launched. Check VM readiness in nook watch. Stop: testing-session.py down")
    except BaseException:
        save(state_file, state)
        (directory / "stop").touch()
        print("Startup interrupted. Stop requested; run down to finish cleanup.", file=sys.stderr)
        raise


def main():
    if len(sys.argv) == 4 and sys.argv[1] == "_worker":
        if sys.argv[3] not in ROLES:
            raise RuntimeError("Unknown worker role")
        worker(Path(sys.argv[2]), sys.argv[3])
        return
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("up", "down", "status"))
    parser.add_argument("--nook", help="nook binary (default: PATH, then repository bin/nook)")
    parser.add_argument("--network", choices=("ask", "full"), default="ask")
    parser.add_argument("--timeout", type=float, default=60, help="cleanup wait in seconds")
    args = parser.parse_args()
    home = Path(os.environ.get("AGENTNOOK_HOME", "~/.agentnook")).expanduser().resolve()
    # Keep host session controls outside projects and profile homes exposed to guests.
    root = home / "testing-session"
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    state_file = root / "active.json"
    with (root / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.action == "up":
            up(args, home, root, state_file)
        elif not state_file.exists():
            print("No testsession recorded.")
        else:
            state = json.loads(state_file.read_text())
            same_server(state)
            directory = Path(state["directory"])
            if args.action == "down":
                down(state, directory, state_file, args.timeout)
            else:
                for role in ROLES:
                    status = "exited" if (directory / (role + ".done")).exists() else (
                        "launched" if (directory / (role + ".started")).exists() else "not started")
                    print(f"{role:10} {state['panes'].get(role, '-'):12} {status}")


if __name__ == "__main__":
    os.umask(0o077)
    try:
        main()
    except (RuntimeError, OSError, ValueError) as error:
        sys.exit(f"testing-session: {error}")
