#!/usr/bin/env python3
"""Generate GitHub release notes from reachable Conventional Commits."""

import argparse
import html
import re
import subprocess

VERSION = re.compile(r"v([0-9]+)\.([0-9]+)\.([0-9]+)(-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?")
HEADER = re.compile(r"([a-z][a-z0-9-]*)(?:\(([^()]+)\))?(!)?: (.+)")
BREAKING = re.compile(r"^BREAKING[ -]CHANGE: (\S.*)$", re.MULTILINE)
SECTIONS = {
    "breaking": "Breaking changes",
    "feat": "Features",
    "fix": "Bug fixes",
    "perf": "Performance",
    "revert": "Reverts",
    "refactor": "Refactoring",
    "docs": "Documentation",
    "test": "Tests",
    "build": "Build",
    "ci": "CI",
    "chore": "Maintenance",
    "style": "Code style",
    "other": "Other changes",
}


def git(*args):
    result = subprocess.run(["git", *args], capture_output=True, encoding="utf-8", errors="replace")
    if result.returncode:
        raise ValueError(result.stderr.strip() or f"git {args[0]} failed")
    return result.stdout


def stable_base(commit, version):
    candidates = []
    for tag in git("tag", "--merged", commit, "--list", "v*").splitlines():
        match = VERSION.fullmatch(tag)
        if match and not match[4]:
            core = tuple(map(int, match.groups()[:3]))
            if core < version:
                candidates.append((core, "+" not in tag, tag))
    return max(candidates)[2] if candidates else None


def plain(text):
    # Treat commit text as plain text, not Markdown or raw HTML.
    escaped = html.escape(text, quote=False).replace("@", "&#64;")
    return re.sub(r"([\\`*_\[\]~])", r"\\\1", escaped)


def release_notes(tag, repository_url):
    match = VERSION.fullmatch(tag)
    if not match:
        raise ValueError("expected a version tag such as v1.2.3 or v1.2.3-rc.1")
    if git("rev-parse", "--is-shallow-repository").strip() == "true":
        raise ValueError("release notes require full Git history and tags")
    commit = git("rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}").strip()
    base = stable_base(commit, tuple(map(int, match.groups()[:3])))
    revision = f"refs/tags/{base}..{commit}" if base else commit
    fields = git("log", "-z", "--no-merges", "--no-show-signature",
                 "--format=%H%x00%s%x00%b", revision, "--").split("\0")
    groups = {key: [] for key in SECTIONS}
    records = iter(fields[:-1])  # Git adds one terminating NUL when the range isn't empty.
    for sha, subject, body in zip(records, records, records, strict=True):
        parsed = HEADER.fullmatch(subject)
        if not parsed:
            continue
        kind, scope, bang, description = parsed.groups()
        notes = BREAKING.findall(body)
        section = "breaking" if bang or notes else kind if kind in SECTIONS else "other"
        message = f"**{plain(scope)}:** " if scope else ""
        message += plain(description)
        if repository_url:
            message += f" ([`{sha[:12]}`]({repository_url}/commit/{sha}))"
        else:
            message += f" (`{sha[:12]}`)"
        groups[section].append("\n".join([f"- {message}", *(f"  - {plain(note)}" for note in notes)]))

    sections = [f"## {title}\n\n" + "\n".join(groups[key]) for key, title in SECTIONS.items() if groups[key]]
    if not sections:
        sections.append("No Conventional Commits in this release.")
    if base and repository_url:
        sections.append(f"**Full changelog:** [{base}...{tag}]({repository_url}/compare/{base}...{tag})")
    return "\n\n".join(sections)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag")
    parser.add_argument("--repository-url", default="", help="GitHub repository URL for commit and comparison links")
    args = parser.parse_args()
    try:
        print(release_notes(args.tag, args.repository_url.rstrip("/")))
    except ValueError as exc:
        parser.exit(1, f"release notes: {exc}\n")


if __name__ == "__main__":
    main()
