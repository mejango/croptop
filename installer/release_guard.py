#!/usr/bin/env python3
"""Read-only release staging gate. Never edits a tag, release, or asset."""

import argparse
import json
import os
import re
import subprocess
from urllib.parse import quote


def api(path, *, allow_missing=False):
    result = subprocess.run(["gh", "api", path], capture_output=True, text=True)
    try:
        value = json.loads(result.stdout)
    except (ValueError, TypeError) as error:
        raise RuntimeError("GitHub returned an unreadable response; refusing release staging") from error
    if result.returncode:
        if allow_missing and isinstance(value, dict) and str(value.get("status")) == "404":
            return None
        raise RuntimeError("GitHub release check failed; refusing release staging")
    return value


def validate_draft(release, tag, *, allow_missing=False):
    if release is None:
        if allow_missing:
            return
        raise ValueError("The staging release does not exist")
    if not isinstance(release, dict) or release.get("tag_name") != tag:
        raise ValueError("The release does not match the requested tag")
    if release.get("draft") is not True:
        raise ValueError("Refusing to stage into an already published release")


def verify_tag(repo, tag, commit, request=api):
    value = request(f"repos/{repo}/git/ref/tags/{quote(tag, safe='')}")
    obj = value.get("object", {})
    for _ in range(8):
        if obj.get("type") == "commit":
            if obj.get("sha") != commit:
                raise ValueError("Remote release tag differs from the checked-out commit")
            return
        if obj.get("type") != "tag" or not re.fullmatch(r"[0-9a-f]{40,64}", obj.get("sha", "")):
            break
        obj = request(f"repos/{repo}/git/tags/{obj['sha']}").get("object", {})
    raise ValueError("Remote release tag does not resolve to the checked-out commit")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag")
    parser.add_argument("--commit", required=True)
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", "mejango/croptop"))
    parser.add_argument("--allow-missing", action="store_true")
    args = parser.parse_args()
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", args.tag):
        parser.error("expected a version tag, such as v0.13.20")
    if not re.fullmatch(r"[0-9a-f]{40,64}", args.commit):
        parser.error("expected the full checked-out commit SHA")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repo):
        parser.error("expected an owner/repository name")
    try:
        verify_tag(args.repo, args.tag, args.commit)
        release = api(f"repos/{args.repo}/releases/tags/{quote(args.tag, safe='')}",
                      allow_missing=args.allow_missing)
        validate_draft(release, args.tag, allow_missing=args.allow_missing)
    except (ValueError, RuntimeError) as error:
        parser.exit(1, f"Release staging refused: {error}\n")
    print(f"Release staging verified for {args.tag} at {args.commit} (draft only).")


if __name__ == "__main__":
    main()
