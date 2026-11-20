#!/usr/bin/env python3
"""Detect changed Docker build inputs without depending on a hosting provider API."""

import os
import subprocess
from pathlib import Path

BACKEND_FILES = {
    "backend/Dockerfile",
    "backend/Dockerfile.dockerignore",
    "backend/go.mod",
    "backend/go.sum",
}
BACKEND_DIRS = ("backend/cmd/", "backend/internal/", "backend/migrations/", "backend/api/")
# Клиенты не деплоятся в self-service — только собираются в CI как проверка/артефакт.
FRONTEND_FILES = set()
FRONTEND_DIRS = ("app/", "packages/")
# Changes to the build pipeline itself must also be validated by building images.
COMMON_FILES = {".forgejo/workflows/ci.yml", ".ci/changes.py"}


def components(paths: list[str]) -> dict[str, bool]:
    common = any(path in COMMON_FILES for path in paths)
    return {
        "backend": common or any(path in BACKEND_FILES or path.startswith(BACKEND_DIRS) for path in paths),
        "frontend": common or any(path in FRONTEND_FILES or path.startswith(FRONTEND_DIRS) for path in paths),
    }


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], text=True)


def changed_paths(event: str, before: str, base: str, head: str) -> list[str]:
    if event == "pull_request":
        # Compare the PR branch with its merge base, not changes on the target branch.
        before = git("merge-base", base, head).strip()
    elif event == "workflow_dispatch":
        # A manual run checks the latest commit, including on non-default branches.
        parents = git("rev-list", "--parents", "-n", "1", head).split()
        before = parents[1] if len(parents) > 1 else ""

    if not before or set(before) == {"0"}:
        return git("ls-tree", "-r", "--name-only", "-z", head).split("\0")[:-1]
    # Include both paths of renames as deletion + addition, and include deleted files.
    return git("diff", "--name-only", "--no-renames", "-z", before, head).split("\0")[:-1]


def main() -> None:
    paths = changed_paths(
        os.environ["CI_EVENT_NAME"],
        os.environ.get("CI_BEFORE_SHA", ""),
        os.environ.get("CI_BASE_SHA", ""),
        os.environ["CI_HEAD_SHA"],
    )
    result = components(paths)
    for name, changed in result.items():
        print(f"{name}: {'changed' if changed else 'unchanged'}")
    output = os.environ.get("GITHUB_OUTPUT")
    if output:
        with Path(output).open("a") as stream:
            for name, changed in result.items():
                stream.write(f"{name}={str(changed).lower()}\n")


if __name__ == "__main__":
    main()
