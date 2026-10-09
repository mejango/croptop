#!/usr/bin/env python3
"""Build a local, pinned Mac pilot. Never creates tags, releases, or appcasts."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import secrets
import shlex
import shutil
import subprocess
import tempfile
from urllib.parse import urlsplit


IDENTITY = "Developer ID Application: Jango De La Noche (SY2W527QJA)"
ROOT = Path(__file__).resolve().parents[1]


class Runner:
    def __init__(self, environment):
        self.environment = environment
        self.redactions = []

    def run(self, *args, cwd=None, capture=False):
        result = subprocess.run([str(arg) for arg in args], cwd=cwd,
                                env=self.environment, capture_output=capture)
        if result.returncode:
            detail = result.stderr.decode(errors="replace") if capture else "see output above"
            for value in self.redactions:
                detail = detail.replace(value, "[redacted]")
            raise RuntimeError(f"{args[0]} failed ({result.returncode}): {detail}")
        return result.stdout if capture else b""


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def validate_origin(value):
    parsed = urlsplit(value)
    try:
        parsed.port
    except ValueError as error:
        raise argparse.ArgumentTypeError("origin has an invalid port") from error
    if (any(c.isspace() for c in value) or parsed.scheme != "https" or not parsed.hostname or parsed.username or
            parsed.password or parsed.path or parsed.query or parsed.fragment):
        raise argparse.ArgumentTypeError("origin must be a bare HTTPS origin")
    return value


def validate_version(value):
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?", value):
        raise argparse.ArgumentTypeError("version must be a three-number version with an optional prerelease suffix")
    return value


def bundle_version(value):
    # Apple permits only X.Y.Z in CFBundleShortVersionString. Keep the full
    # prerelease version in the engine, archive names, and provenance instead.
    return validate_version(value).split("-", 1)[0]


def signing(runner, saved, temporary, action):
    password = secrets.token_urlsafe(32)
    runner.redactions.append(password)
    runner.environment["CROPTOP_TEMP_P12_PASSWORD"] = password
    certificate = runner.run("openssl", "x509", "-in", saved / "devid.cer.pem",
                             "-pubkey", "-noout", capture=True)
    public = runner.run("openssl", "pkey", "-in", saved / "devid.key", "-passin", "pass:",
                       "-pubout", capture=True)
    if certificate != public:
        raise RuntimeError("exported signing key does not match Developer ID certificate")
    keychain = temporary / "pilot.keychain-db"
    identity = temporary / "identity.p12"
    created = False
    try:
        runner.run("openssl", "pkcs12", "-export", "-inkey", saved / "devid.key",
                   "-in", saved / "devid.cer.pem", "-out", identity,
                   "-keypbe", "PBE-SHA1-3DES", "-certpbe", "PBE-SHA1-3DES", "-macalg", "sha1",
                   "-passin", "pass:", "-passout", "env:CROPTOP_TEMP_P12_PASSWORD", capture=True)
        identity.chmod(0o600)
        runner.run("security", "create-keychain", "-p", password, keychain, capture=True)
        created = True
        runner.run("security", "set-keychain-settings", "-lut", "7200", keychain, capture=True)
        runner.run("security", "unlock-keychain", "-p", password, keychain, capture=True)
        runner.run("security", "import", identity, "-k", keychain, "-P", password,
                   "-T", "/usr/bin/codesign", capture=True)
        identity.unlink()
        runner.run("security", "set-key-partition-list", "-S", "apple-tool:,apple:,codesign:",
                   "-s", "-k", password, keychain, capture=True)
        # macOS still needs this search-list entry to resolve the certificate's
        # private key even with codesign --keychain. Only add our temporary
        # entry, and remove only that entry in finally (preserve concurrent edits).
        current = shlex.split(runner.run("security", "list-keychains", "-d", "user", capture=True).decode())
        runner.run("security", "list-keychains", "-d", "user", "-s", keychain,
                   *[item for item in current if item != str(keychain)], capture=True)
        probe = temporary / "signing-probe"
        shutil.copyfile("/usr/bin/true", probe)
        probe.chmod(0o755)
        runner.run("/usr/bin/codesign", "--force", "--options", "runtime", "--timestamp",
                   "--keychain", keychain, "--sign", IDENTITY, probe, capture=True)
        runner.run("/usr/bin/codesign", "--verify", "--strict", probe, capture=True)
        print("Developer ID signing probe passed using an isolated temporary keychain.", flush=True)
        # Keep the stable installer unchanged while selecting this keychain explicitly.
        shim = temporary / "bin"
        shim.mkdir()
        shim_code = '#!/bin/sh\nfor arg do\n  if [ "$arg" = "--sign" ]; then\n    exec /usr/bin/codesign --keychain "$CROPTOP_RELEASE_KEYCHAIN" "$@"\n  fi\ndone\nexec /usr/bin/codesign "$@"\n'
        (shim / "codesign").write_text(shim_code)
        (shim / "codesign").chmod(0o755)
        runner.environment["CROPTOP_RELEASE_KEYCHAIN"] = str(keychain)
        runner.environment["MACOS_SIGN_IDENTITY"] = IDENTITY
        runner.environment["PATH"] = str(shim) + os.pathsep + runner.environment.get("PATH", "")
        action()
    finally:
        if created:
            current = shlex.split(runner.run("security", "list-keychains", "-d", "user", capture=True).decode())
            if str(keychain) in current:
                runner.run("security", "list-keychains", "-d", "user", "-s",
                           *[item for item in current if item != str(keychain)], capture=True)
            runner.run("security", "delete-keychain", keychain, capture=True)
            print("Temporary signing keychain removed; original signing keychain preserved.", flush=True)


def build(runner, args, temporary):
    if runner.run("git", "status", "--porcelain", "--untracked-files=all", cwd=ROOT, capture=True):
        raise RuntimeError("commit the pilot source first; refusing to build a dirty or untracked source tree")
    commit = runner.run("git", "rev-parse", "HEAD", cwd=ROOT, capture=True).decode().strip()
    if args.commit and commit != args.commit:
        raise RuntimeError("source HEAD differs from --commit")
    output = args.out.resolve()
    if output.exists():
        raise RuntimeError("use a fresh output directory; existing artifacts will not be overwritten")
    output.mkdir(parents=True)
    source = temporary / "source"
    source.mkdir()
    source_archive = output / "source.tar.gz"
    runner.run("git", "archive", "--format=tar.gz", "--output", source_archive, commit, cwd=ROOT)
    runner.run("tar", "-xzf", source_archive, "-C", source)
    archives = output / "in"
    archives.mkdir()
    package_in = temporary / "package-in"
    package_in.mkdir()
    app_version = bundle_version(args.version)
    runner.environment["CGO_ENABLED"] = "0"
    runner.environment["GOOS"] = "darwin"
    runner.environment["CROPTOP_BUILD_NUMBER"] = str(args.build)
    # The reused installer creates its own build directory; keep that generated
    # material inside this task's automatically cleaned temporary directory too.
    runner.environment["TMPDIR"] = str(temporary) + os.sep
    flags = (f"-s -w -X main.version={args.version} "
             f"-X github.com/mejango/croptop/internal/server.defaultMobileOrigin={args.origin}")
    for arch in ("arm64", "amd64"):
        binary_dir = temporary / arch
        binary_dir.mkdir()
        runner.environment["GOARCH"] = arch
        runner.run("go", "build", "-trimpath", "-ldflags", flags, "-o", binary_dir / "croptop",
                   "./cmd/croptop", cwd=source)
        archive = archives / f"croptop_{args.version}_darwin_{arch}.tar.gz"
        runner.run("tar", "-czf", archive, "-C", binary_dir, "croptop")
        os.link(archive, package_in / f"croptop_{app_version}_darwin_{arch}.tar.gz")
    runner.environment.pop("GOARCH", None)
    runner.environment.pop("GOOS", None)
    runner.run("sh", source / "installer/macos.sh", app_version, package_in, output / "out", cwd=source)
    app = output / "out/Croptop.app"
    dmg = output / "out/Croptop.dmg"
    runner.run("codesign", "--verify", "--deep", "--strict", app)
    runner.run("spctl", "--assess", "--type", "execute", "--verbose=2", app)
    for item in (app, dmg):
        runner.run("xcrun", "stapler", "validate", item)
    with (app / "Contents/Info.plist").open("rb") as stream:
        info = plistlib.load(stream)
    if info["CFBundleVersion"] != str(args.build) or info["CFBundleShortVersionString"] != app_version:
        raise RuntimeError("packaged app version does not match requested pilot")
    packaged_version = runner.run(app / "Contents/Resources/croptop", "version", capture=True).decode().strip()
    if args.version not in packaged_version:
        raise RuntimeError("packaged engine version does not match requested pilot")
    provenance = {
        "version": args.version, "bundleVersion": app_version, "build": args.build, "commit": commit,
        "origin": args.origin, "sourceSHA256": digest(source_archive), "dmgSHA256": digest(dmg),
        "go": runner.run("go", "version", capture=True).decode().strip(),
        "xcode": runner.run("xcodebuild", "-version", capture=True).decode().strip(),
        "engineVersion": packaged_version, "signingIdentity": IDENTITY,
        "notarized": True, "uploaded": False,
        "stableAppcastChanged": False,
    }
    (output / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
    print(f"Verified notarized pilot: {dmg}\nSHA256: {provenance['dmgSHA256']}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("probe", "build"))
    parser.add_argument("--signing-backup", type=Path,
                        default=Path.home() / "Documents/croptop-signing")
    parser.add_argument("--version", type=validate_version)
    parser.add_argument("--build", type=int)
    parser.add_argument("--origin", type=validate_origin)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--commit")
    args = parser.parse_args()
    if args.action == "build":
        if not all((args.version, args.build, args.origin, args.out)) or args.build < 1:
            parser.error("build requires --version, positive --build, --origin, and --out")
        if not (os.environ.get("NOTARY_PROFILE") or all(os.environ.get(k) for k in
                ("AC_API_KEY_PATH", "AC_API_KEY_ID", "AC_API_ISSUER_ID"))):
            parser.error("build requires existing notarization credentials; unsigned pilots are not distributable")
    runner = Runner(os.environ.copy())
    with tempfile.TemporaryDirectory(prefix="croptop-pilot-signing-", dir="/private/tmp") as directory:
        temporary = Path(directory)
        signing(runner, args.signing_backup, temporary,
                (lambda: build(runner, args, temporary)) if args.action == "build" else (lambda: None))


if __name__ == "__main__":
    main()
