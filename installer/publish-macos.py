#!/usr/bin/env python3
"""Prepare and publish an already signed/notarized Mac build. Never creates a tag."""
import argparse, hashlib, json, os, pathlib, plistlib, shutil, subprocess, tempfile
import xml.etree.ElementTree as ET

REPO = "mejango/croptop"
HERE = pathlib.Path(__file__).resolve().parent
SPARKLE_VERSION = "2.9.6"
SPARKLE_SHA256 = "52bf9e88cdd972fc0c81501377a880e90d47031bd8ca5462488f843e2609e192"

def run(*args, **kw):
    return subprocess.run([str(a) for a in args], check=True, **kw)

def digest(path):
    with open(path, "rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()

def tools(directory):
    archive = directory / "Sparkle.tar.xz"
    run("curl", "--fail", "--location", "--silent", "--show-error", "--retry", "3", "--output", archive,
        f"https://github.com/sparkle-project/Sparkle/releases/download/{SPARKLE_VERSION}/Sparkle-{SPARKLE_VERSION}.tar.xz")
    if digest(archive) != SPARKLE_SHA256:
        raise SystemExit("Sparkle tools checksum mismatch")
    run("tar", "-xf", archive, "-C", directory)
    return directory / "bin"

def prepare(version, out, key):
    app = out / "Croptop.app"
    dmg = out / "Croptop.dmg"
    with (app / "Contents/Info.plist").open("rb") as stream:
        info = plistlib.load(stream)
    build = info["CFBundleVersion"]
    assert build.isdecimal(), "Build number must be numeric"
    assert info["CFBundleShortVersionString"] == version
    assert info["SUPublicEDKey"] == (HERE / "sparkle-public-key.txt").read_text().strip()
    assert info["SUFeedURL"] == f"https://github.com/{REPO}/releases/latest/download/appcast.xml"
    assert info["SURequireSignedFeed"] and info["SUVerifyUpdateBeforeExtraction"]
    run("codesign", "--verify", "--deep", "--strict", app)
    run("spctl", "--assess", "--type", "execute", app)
    for file in (app, dmg):
        run("xcrun", "stapler", "validate", file)
    feed = out / "update"
    feed.mkdir(exist_ok=True)
    archive = feed / f"Croptop-{build}.dmg"
    if archive.exists() and digest(archive) != digest(dmg):
        raise SystemExit("Existing archive has different content; increment the build number")
    shutil.copy2(dmg, archive)
    with tempfile.TemporaryDirectory(prefix="croptop-sparkle-tools-") as directory:
        binary = tools(pathlib.Path(directory))
        run(binary / "generate_appcast", "--ed-key-file", key, "--maximum-deltas", "0",
            "--download-url-prefix", f"https://github.com/{REPO}/releases/download/v{version}/",
            "--link", "https://crop.top/install", feed)
        run(binary / "sign_update", "--verify", "--ed-key-file", key, feed / "appcast.xml")
    item = ET.parse(feed / "appcast.xml").find("./channel/item")
    ns = "{http://www.andymatuschak.org/xml-namespaces/sparkle}"
    assert item.findtext(ns + "version") == build
    assert item.find("enclosure").get("url").endswith("/" + archive.name)
    assert item.find("enclosure").get(ns + "edSignature")
    return build, archive, feed / "appcast.xml"

def publish(version, out, build, archive, feed):
    latest = json.loads(run("gh", "api", f"repos/{REPO}/releases/latest", capture_output=True).stdout)
    if latest["tag_name"] != "v" + version:
        raise SystemExit("Target must be the latest existing release; no tag or release was changed")
    assets = {asset["name"]: asset for asset in latest["assets"]}
    with tempfile.TemporaryDirectory(prefix="croptop-feed-check-") as directory:
        directory = pathlib.Path(directory)
        if "appcast.xml" in assets:
            run("gh", "release", "download", "v" + version, "--repo", REPO, "--pattern", "appcast.xml", "--dir", directory)
            versions = [int(node.text) for node in ET.parse(directory / "appcast.xml").iter("{http://www.andymatuschak.org/xml-namespaces/sparkle}version")]
            if versions and max(versions) > int(build):
                raise SystemExit("Refusing to publish an older build")
        if archive.name in assets:
            run("gh", "release", "download", "v" + version, "--repo", REPO, "--pattern", archive.name, "--dir", directory)
            if digest(directory / archive.name) != digest(archive):
                raise SystemExit("Build already published with different bytes; increment the build number")
        else:
            run("gh", "release", "upload", "v" + version, archive, "--repo", REPO)
        # Publish the feed last, once its immutable archive is available.
        run("gh", "release", "upload", "v" + version, out / "Croptop.dmg", "--clobber", "--repo", REPO)
        run("gh", "release", "upload", "v" + version, feed, "--clobber", "--repo", REPO)
    print(f"Published Croptop {version} build {build}: {digest(archive)}")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("out", type=pathlib.Path)
    parser.add_argument("--prepare-only", action="store_true")
    args = parser.parse_args()
    key = os.environ.get("SPARKLE_KEY_FILE")
    if not key or not pathlib.Path(key).is_file():
        parser.error("Set SPARKLE_KEY_FILE to the private update signing key outside the repository")
    result = prepare(args.version, args.out.resolve(), key)
    if not args.prepare_only:
        publish(args.version, args.out.resolve(), *result)
