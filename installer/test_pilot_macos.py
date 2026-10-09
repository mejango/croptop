import argparse
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("pilot_macos", Path(__file__).with_name("pilot-macos.py"))
PILOT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PILOT)


class FakeRunner:
    def __init__(self):
        self.environment = {}
        self.redactions = []
        self.calls = []
        self.keychains = ["/Users/test/Library/Keychains/login.keychain-db"]

    def run(self, *args, **kwargs):
        args = tuple(str(arg) for arg in args)
        self.calls.append(args)
        if args[:2] == ("openssl", "pkcs12"):
            Path(args[args.index("-out") + 1]).touch()
        if args[:2] == ("security", "list-keychains"):
            if "-s" in args:
                self.keychains = list(args[args.index("-s") + 1:])
                return b""
            return "\n".join('"' + k + '"' for k in self.keychains).encode()
        if args[:3] == ("git", "status", "--porcelain"):
            return b" M file.go\n"
        return b"matching-public-key"


class PilotTests(unittest.TestCase):
    def test_origin_requires_exact_https_origin(self):
        self.assertEqual(PILOT.validate_origin("https://phone.example"), "https://phone.example")
        for bad in ("http://phone.example", "https://phone.example/", "https://user@phone.example",
                    "https://phone.example#fragment", "https://phone.example?query",
                    "https://phone.example\n", "https://phone.example:nope"):
            with self.subTest(origin=bad), self.assertRaises(argparse.ArgumentTypeError):
                PILOT.validate_origin(bad)

    def test_version_cannot_escape_archive_paths(self):
        self.assertEqual(PILOT.validate_version("0.13.20-phone.1"), "0.13.20-phone.1")
        for bad in ("", "../../app", "1; touch file", "1.2", "1.2.3\n"):
            with self.subTest(version=bad), self.assertRaises(argparse.ArgumentTypeError):
                PILOT.validate_version(bad)

    def test_bundle_version_is_numeric_while_prerelease_is_preserved(self):
        self.assertEqual(PILOT.bundle_version("0.13.20-phone.1"), "0.13.20")
        self.assertEqual(PILOT.bundle_version("0.13.20"), "0.13.20")

    def test_signing_probe_uses_explicit_temp_keychain_and_cleans_on_failure(self):
        runner = FakeRunner()
        with tempfile.TemporaryDirectory() as folder:
            temporary = Path(folder)
            def fail():
                raise RuntimeError("build failed")
            with self.assertRaisesRegex(RuntimeError, "build failed"):
                PILOT.signing(runner, Path("/not-real-signing-backup"), temporary, fail)
            keychain = str(temporary / "pilot.keychain-db")
            sign = next(c for c in runner.calls if c[0] == "/usr/bin/codesign" and "--sign" in c)
            self.assertEqual(sign[sign.index("--keychain") + 1], keychain)
            self.assertIn(("security", "delete-keychain", keychain), runner.calls)
            self.assertEqual(runner.keychains, ["/Users/test/Library/Keychains/login.keychain-db"])
            self.assertEqual([c for c in runner.calls if c[:2] == ("security", "delete-keychain")],
                             [("security", "delete-keychain", keychain)])
            self.assertFalse(any(c[0] == "gh" for c in runner.calls))

    def test_build_refuses_dirty_tree_before_creating_artifacts(self):
        runner = FakeRunner()
        with tempfile.TemporaryDirectory() as folder:
            with self.assertRaisesRegex(RuntimeError, "dirty or untracked"):
                PILOT.build(runner, argparse.Namespace(), Path(folder))
            self.assertEqual(list(Path(folder).iterdir()), [])


if __name__ == "__main__":
    unittest.main()
