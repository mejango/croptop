#!/usr/bin/env python3
"""Read-only release staging gate. Never edits a tag, release, or asset."""

import argparse
import json
import os
import re
import subprocess
from urllib.parse import quote


def api(path):
    result = subprocess.run(["gh", "api", path], capture_output=True, text=True)
    try:
        value = json.loads(result.stdout)
    except (ValueError, TypeError) as error:
        raise RuntimeError("GitHub returned an unreadable response; refusing release staging") from error
    if result.returncode:
        raise RuntimeError("GitHub release check failed; refusing release staging")
    return value


def find_release(repo, tag, request=api):
    # The tag endpoint returns published releases only. An authenticated list
    # includes drafts; a failed/404 request must never imply draft absence.
    matches = []
    for page in range(1, 1001):
        releases = request(f"repos/{repo}/releases?per_page=100&page={page}")
        if not isinstance(releases, list) or any(not isinstance(r, dict) for r in releases):
            raise RuntimeError("GitHub returned an invalid releases list")
        matches.extend(r for r in releases if r.get("tag_name") == tag)
        if len(matches) > 1:
            raise ValueError("Multiple releases match the requested tag")
        if len(releases) < 100:
            return matches[0] if matches else None
    raise RuntimeError("Release pagination exceeded the safety limit")


def validate_draft(release, tag, *, allow_missing=False, commit=None, release_id=None):
    if release is None:
        if allow_missing:
            return
        raise ValueError("The staging release does not exist")
    if not isinstance(release, dict) or release.get("tag_name") != tag:
        raise ValueError("The release does not match the requested tag")
    if release.get("draft") is not True:
        raise ValueError("Refusing to stage into an already published release")
    if commit is not None and release.get("target_commitish") != commit:
        raise ValueError("Draft target differs from the checked-out commit")
    if release_id is not None and release.get("id") != release_id:
        raise ValueError("Draft release ID differs from the pinned staging release")


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
    parser.add_argument("--release-id", type=int)
    parser.add_argument("--json", action="store_true", help="emit verified staging metadata")
    args = parser.parse_args()
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", args.tag):
        parser.error("expected a version tag, such as v0.13.20")
    if not re.fullmatch(r"[0-9a-f]{40,64}", args.commit):
        parser.error("expected the full checked-out commit SHA")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repo):
        parser.error("expected an owner/repository name")
    try:
        verify_tag(args.repo, args.tag, args.commit)
        release = find_release(args.repo, args.tag)
        validate_draft(release, args.tag, allow_missing=args.allow_missing,
                       commit=args.commit, release_id=args.release_id)
    except (ValueError, RuntimeError) as error:
        parser.exit(1, f"Release staging refused: {error}\n")
    if args.json:
        print(json.dumps({key: release[key] for key in
                          ("id", "tag_name", "draft", "target_commitish", "assets")} if release else None))
    else:
        print(f"Release staging verified for {args.tag} at {args.commit} (draft only).")


if __name__ == "__main__":
    main()
