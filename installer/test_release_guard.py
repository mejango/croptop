import importlib.util
import json
from pathlib import Path
import re
import subprocess
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("release_guard", ROOT / "installer/release_guard.py")
GUARD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GUARD)


def section(text, name):
    match = re.search(rf"(?m)^{re.escape(name)}:\s*\n", text)
    if not match:
        raise AssertionError(f"missing {name} section")
    remaining = text[match.end():]
    end = re.search(r"(?m)^\S[^\n]*:\s*", remaining)
    return remaining[:end.start()] if end else remaining


class ReleaseGuardTests(unittest.TestCase):
    def test_only_matching_drafts_are_writable(self):
        GUARD.validate_draft({"tag_name": "v0.13.20", "draft": True}, "v0.13.20")
        for release in ({"tag_name": "v0.13.19", "draft": True},
                        {"tag_name": "v0.13.20", "draft": False},
                        {"tag_name": "v0.13.20", "draft": False, "prerelease": True},
                        {"tag_name": "v0.13.20", "draft": "true"}, {}, None):
            with self.subTest(release=release), self.assertRaises(ValueError):
                GUARD.validate_draft(release, "v0.13.20")

    def test_only_initial_staging_accepts_missing_release(self):
        GUARD.validate_draft(None, "v0.13.20", allow_missing=True)
        with self.assertRaises(ValueError):
            GUARD.validate_draft({"tag_name": "v0.13.20", "draft": False},
                                 "v0.13.20", allow_missing=True)

    def test_network_or_auth_failure_never_means_missing(self):
        for status in ("401", "403", "500"):
            result = subprocess.CompletedProcess([], 1, json.dumps({"status": status}), "")
            with self.subTest(status=status), patch.object(GUARD.subprocess, "run", return_value=result), self.assertRaises(RuntimeError):
                GUARD.api("repos/example/app/releases/tags/v1.0.0", allow_missing=True)
        result = subprocess.CompletedProcess([], 1, '{"status":"404"}', "")
        with patch.object(GUARD.subprocess, "run", return_value=result):
            self.assertIsNone(GUARD.api("missing", allow_missing=True))
            with self.assertRaises(RuntimeError):
                GUARD.api("missing")
        result = subprocess.CompletedProcess([], 1, "", "network failed")
        with patch.object(GUARD.subprocess, "run", return_value=result), self.assertRaises(RuntimeError):
            GUARD.api("unreachable", allow_missing=True)

    def test_tag_must_resolve_to_exact_checked_out_commit(self):
        commit, tag = "a" * 40, "b" * 40
        GUARD.verify_tag("owner/repo", "v1.0.0", commit,
                         lambda _: {"object": {"type": "commit", "sha": commit}})
        requests = []
        def annotated(path):
            requests.append(path)
            return {"object": {"type": "tag", "sha": tag}} if len(requests) == 1 else {"object": {"type": "commit", "sha": commit}}
        GUARD.verify_tag("owner/repo", "v1.0.0", commit, annotated)
        self.assertEqual(requests[-1], f"repos/owner/repo/git/tags/{tag}")
        with self.assertRaises(ValueError):
            GUARD.verify_tag("owner/repo", "v1.0.0", commit,
                             lambda _: {"object": {"type": "commit", "sha": "c" * 40}})
        with self.assertRaises(ValueError):
            GUARD.verify_tag("owner/repo", "v1.0.0", commit,
                             lambda _: {"object": {"type": "tag", "sha": tag}})

    def test_goreleaser_stages_without_replacing_or_promoting(self):
        config = (ROOT / ".goreleaser.yaml").read_text()
        release = section(config, "release")
        for setting in ("draft: true", "make_latest: false", "use_existing_draft: true",
                        "replace_existing_draft: false", "replace_existing_artifacts: false"):
            self.assertRegex(release, rf"(?m)^  {re.escape(setting)}\s*$")
        self.assertIn('target_commitish: "{{ .Commit }}"', release)
        self.assertRegex(section(config, "brews"), r"(?m)^    skip_upload: true\s*$")
        self.assertNotIn("HOMEBREW_TAP_GITHUB_TOKEN", config)
        self.assertIn("goos: [darwin, linux, windows]", section(config, "builds"))
        self.assertIn("goarch: [amd64, arm64]", section(config, "builds"))

    def test_workflow_has_no_automatic_stable_or_unsigned_mac_writer(self):
        workflow = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertIn("args: release --clean --draft", workflow)
        self.assertIn("cancel-in-progress: false", workflow)
        self.assertIn("test_release_guard.py", workflow)
        self.assertIn("timeout-minutes: 30", workflow)
        for gate in ("go test ./...", "go vet ./..."):
            self.assertLess(workflow.index(gate), workflow.index("uses: goreleaser/goreleaser-action@"))
        self.assertEqual(workflow.count("installer/release_guard.py"), 4)
        self.assertIn("signed-mac-handoff:", workflow)
        self.assertIn("needs: [goreleaser, windows-installer]", workflow)
        for forbidden in ("gh release edit", "--clobber", "HOMEBREW_TAP_GITHUB_TOKEN",
                          "installer/macos.sh", "installer/publish-macos.py", "macos-app:"):
            self.assertNotIn(forbidden, workflow)
        self.assertIn('foreach ($arch in @("amd64", "arm64"))', workflow)
        build = (ROOT / "installer/macos-build-number").read_text().strip()
        self.assertTrue(build.isdecimal())
        self.assertGreaterEqual(int(build), 1161)


if __name__ == "__main__":
    unittest.main()
