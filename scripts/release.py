#!/usr/bin/env python3
"""Validate repository versions and create local release commits/tags. Never push."""
import argparse
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
PROJECT = Path("Maccy.xcodeproj/project.pbxproj")
VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)")


def validate(value):
    if not VERSION.fullmatch(value):
        raise ValueError("Version must be vMAJOR.MINOR.PATCH without whitespace or leading zeros")
    return value


def read_env(path):
    text = path.read_text()
    lines = [line for line in text.splitlines() if re.match(r"^\s*(?:export\s+)?version\s*=", line)]
    if len(lines) != 1 or not lines[0].startswith("version="):
        raise ValueError(f"{path}: expected exactly one version=vMAJOR.MINOR.PATCH line")
    return text, validate(lines[0][8:])


def field(text, name):
    pattern = re.compile(rf"(?m)^(\s*{name} = )([^;\n]+)(;)$")
    values = [match[1] for match in pattern.findall(text)]
    if len(values) != 2 or len(set(values)) != 1:
        raise ValueError(f"Expected matching Debug/Release {name} fields")
    return pattern, values[0]


def check(root, env_file, expected=""):
    env_text, version = read_env(env_file)
    example_text, example_version = read_env(root / ".env.example")
    project_text = (root / PROJECT).read_text()
    _, marketing = field(project_text, "MARKETING_VERSION")
    _, build = field(project_text, "CURRENT_PROJECT_VERSION")
    if not re.fullmatch(r"[1-9][0-9]*", build):
        raise ValueError("CURRENT_PROJECT_VERSION must be a positive integer")
    if version != example_version or version != "v" + marketing:
        raise ValueError("Version mismatch between .env, .env.example and Xcode project")
    if expected and validate(expected) != version:
        raise ValueError(f"Tag {expected} does not match repository version {version}")
    return version, build, env_text, example_text, project_text


def git(root, *args):
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def release(root, env_file, requested=""):
    version, build, env_text, example_text, project_text = check(root, env_file)
    if git(root, "status", "--porcelain", "--untracked-files=all"):
        raise ValueError("Release requires a clean Git worktree, including untracked files")
    git(root, "symbolic-ref", "--quiet", "HEAD")
    if requested:
        target = validate(requested)
    else:
        major, minor, patch = VERSION.fullmatch(version).groups()
        target = f"v{major}.{minor}.{int(patch) + 1}"
    if tuple(map(int, target[1:].split('.'))) <= tuple(map(int, version[1:].split('.'))):
        raise ValueError("Release version must be greater than the current version")
    if git(root, "tag", "--list", target):
        raise ValueError(f"Tag {target} already exists")
    git(root, "var", "GIT_AUTHOR_IDENT")
    git(root, "var", "GIT_COMMITTER_IDENT")
    for name, value in (("MARKETING_VERSION", target[1:]), ("CURRENT_PROJECT_VERSION", str(int(build) + 1))):
        pattern, _ = field(project_text, name)
        project_text = pattern.sub(lambda m: m[1] + value + m[3], project_text)
    updates = {
        env_file: re.sub(r"(?m)^version=[^\n]+$", f"version={target}", env_text),
        root / ".env.example": re.sub(r"(?m)^version=[^\n]+$", f"version={target}", example_text),
        root / PROJECT: project_text,
    }
    for path, content in updates.items():
        path.write_text(content)
    check(root, env_file, target)
    files = [".env.example", str(PROJECT)]
    relative_env = str(env_file.relative_to(root))
    if git(root, "ls-files", "--", relative_env):
        files.append(relative_env)
    git(root, "add", "--", *files)
    # Leave failed commits/tags visible for recovery; never reset user or hook changes.
    git(root, "commit", "-m", f"chore: release {target}", "--", *files)
    check(root, env_file, target)
    if git(root, "status", "--porcelain", "--untracked-files=all"):
        raise ValueError("Worktree changed during release commit; tag was not created")
    git(root, "tag", "-a", target, "-m", f"Release {target}")
    print(f"Created local release commit and annotated tag {target}. Nothing was pushed.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("check", "release"))
    parser.add_argument("--env-file", default=".env")
    parser.add_argument("--version", default="")
    args = parser.parse_args()
    env_file = (ROOT / args.env_file).resolve()
    if not env_file.is_relative_to(ROOT):
        raise ValueError("ENV_FILE must be inside this repository")
    if args.command == "release":
        release(ROOT, env_file, args.version)
    else:
        print(check(ROOT, env_file, args.version)[0])


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        sys.exit(f"release: {error}")
