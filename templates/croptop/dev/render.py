#!/usr/bin/env python3
"""Render the croptop templates the way Planet does, without Planet.

    python3 dev/render.py [--site DIR] [--out DIR]

--site  Folder with planet.json, optional templateSettings.json, and one
        <article-id>/ dir per article (article.json, nft.json.cid.txt, attachments).
        Defaults to dev/fixture. A site Planet already published works too:
        ~/Library/Containers/xyz.planetable.Lite/Data/Documents/Planet/Public/<id>
--out   Output folder. Defaults to dev/out.

Settings come from template.json defaults, overridden by the site's
templateSettings.json, overridden by dev/settings.local.json (gitignored) so you
can point the dev site at your own collection addresses.

Needs jinja2:  python3 -m venv dev/.venv && dev/.venv/bin/pip install jinja2
"""
import argparse, datetime, hashlib, json, os, re, shutil, sys, time

try:
    import jinja2
except ImportError:
    sys.exit(__doc__.strip().splitlines()[-1])

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
APPLE_EPOCH = 978307200  # Planet stores seconds since 2001-01-01.


class AppleDate(float):
    """Planet's Date: a float in Apple epoch that also exposes .timeIntervalSince1970."""
    @property
    def timeIntervalSince1970(self):
        return float(self) + APPLE_EPOCH

    def datetime(self):
        return datetime.datetime.fromtimestamp(self.timeIntervalSince1970)


# --- Stencil -> Jinja2 ------------------------------------------------------
# The templates are Stencil (Swift). Jinja2 reads the same syntax after these
# rewrites; anything else Planet-specific goes in filters/context below.
STENCIL_REWRITES = [
    (re.compile(r"\.count\b"), "|length"),
    (re.compile(r"!= nil\b"), "is not none"),
]


class StencilLoader(jinja2.FileSystemLoader):
    def get_source(self, environment, template):
        source, filename, uptodate = super().get_source(environment, template)
        for pattern, repl in STENCIL_REWRITES:
            source = pattern.sub(repl, source)
        return source, filename, uptodate


def js_literal(value):
    # Stencil prints Swift Bools as true/false; the templates splice them into JS.
    if value is True: return "true"
    if value is False: return "false"
    return value


def make_env():
    env = jinja2.Environment(loader=StencilLoader(os.path.join(ROOT, "templates")), autoescape=False, finalize=js_literal)
    env.filters["mdyydot"] = lambda d: d.datetime().strftime("%-m.%-d.%y")
    env.filters["formatDateC"] = lambda d: d.datetime().isoformat()
    return env


# --- Site data -------------------------------------------------------------
def load_settings(site):
    with open(os.path.join(ROOT, "template.json")) as f:
        settings = {k: v["defaultValue"] for k, v in json.load(f)["settings"].items()}
    for path in (os.path.join(site, "templateSettings.json"), os.path.join(ROOT, "dev", "settings.local.json")):
        if os.path.exists(path):
            with open(path) as f:
                settings.update(json.load(f))
    return settings


def load_article(site, entry):
    article = dict(entry)
    path = os.path.join(site, entry["id"], "article.json")
    if os.path.exists(path):
        with open(path) as f:
            article.update(json.load(f))
    article["created"] = AppleDate(article["created"])
    article.setdefault("slug", "")
    article.setdefault("externalLink", "")
    article.setdefault("videoFilename", "")
    article.setdefault("audioFilename", "")
    article.setdefault("heroImageFilename", "")
    article.setdefault("articleType", 0)
    return article


def summary(article):
    return re.sub(r"\s+", " ", article.get("content", ""))[:200]


def base_context(planet, settings, site, assets_prefix):
    with open(os.path.join(ROOT, "assets", "style.css"), "rb") as f:
        css_sha = hashlib.sha256(f.read()).hexdigest()
    pages = [a for a in planet["articles"] if a.get("articleType") == 1]
    return {
        "assets_prefix": assets_prefix,
        "style_css_sha256": css_sha,
        "build_timestamp": int(time.time()),
        "planet": planet,
        "page_title": planet["name"],
        "page_description": planet.get("about", ""),
        "page_description_html": planet.get("about", ""),
        "has_avatar": os.path.exists(os.path.join(site, "avatar.png")),
        "og_image_url": "",
        "user_settings": settings,
        "custom_code_head": "",
        "custom_code_body_start": "",
        "custom_code_body_end": "",
        "site_navigation": [{"title": p["title"], "slug": p.get("slug") or p["id"], "externalLink": p.get("externalLink", "")} for p in pages],
        "current_item_type": "index",
        "tag_key": None,
        "article": {"slug": None},
    }


def render_site(site, out):
    env = make_env()
    with open(os.path.join(site, "planet.json")) as f:
        planet = json.load(f)
    settings = load_settings(site)

    if os.path.exists(out):
        shutil.rmtree(out)
    os.makedirs(out)
    shutil.copytree(os.path.join(ROOT, "assets"), os.path.join(out, "assets"))
    for name in ("planet.json", "avatar.png", "favicon.ico", "rss.xml"):
        if os.path.exists(os.path.join(site, name)):
            shutil.copy(os.path.join(site, name), out)
    with open(os.path.join(out, "templateSettings.json"), "w") as f:
        json.dump(settings, f, indent=2)
    shutil.copy(os.path.join(ROOT, "dev", "simchain.js"), out)  # dev-only virtual chain, see its header

    with open(os.path.join(out, "index.html"), "w") as f:
        f.write(env.get_template("index.html").render(base_context(planet, settings, site, "./")))

    for entry in planet["articles"]:
        article = load_article(site, entry)
        src, dst = os.path.join(site, entry["id"]), os.path.join(out, entry["id"])
        if os.path.exists(src):
            shutil.copytree(src, dst, ignore=shutil.ignore_patterns("index.html", "simple.html"))
        else:
            os.makedirs(dst)
        ctx = base_context(planet, settings, site, "../")
        ctx.update({
            "article": article,
            "article_summary": summary(article),
            "article_type": article["articleType"],
            "content_html": article.get("contentRendered", ""),
            "social_image_url": "",
            "page_title": article.get("title") or planet["name"],
            "current_item_type": "blog",
        })
        with open(os.path.join(dst, "index.html"), "w") as f:
            f.write(env.get_template("blog.html").render(ctx))
        with open(os.path.join(dst, "simple.html"), "w") as f:
            f.write(env.get_template("simple.html").render(ctx))
    return len(planet["articles"])


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--site", default=os.path.join(ROOT, "dev", "fixture"))
    p.add_argument("--out", default=os.path.join(ROOT, "dev", "out"))
    args = p.parse_args(argv)
    n = render_site(os.path.abspath(args.site), os.path.abspath(args.out))
    print(f"rendered index + {n} articles -> {args.out}")


if __name__ == "__main__":
    main()
