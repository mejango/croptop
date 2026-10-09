#!/usr/bin/env python3
"""Serve the rendered dev site on http://localhost:8642 and re-render on change.

    python3 dev/serve.py [--site DIR] [--port N]

Any edit under templates/, assets/, template.json or the site folder is picked
up on the next request. See render.py for --site.
"""
import argparse, functools, http.server, os, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import render

WATCH = [os.path.join(render.ROOT, d) for d in ("templates", "assets", "template.json")]


def newest_mtime(paths):
    latest = 0
    for path in paths:
        for dirpath, _, files in os.walk(path) if os.path.isdir(path) else [(os.path.dirname(path), [], [os.path.basename(path)])]:
            for name in files:
                latest = max(latest, os.stat(os.path.join(dirpath, name)).st_mtime)
    return latest


class Handler(http.server.SimpleHTTPRequestHandler):
    rendered_at = 0

    def do_GET(self):
        stamp = newest_mtime(WATCH + [self.site])
        if stamp > Handler.rendered_at:
            render.render_site(self.site, self.out)
            Handler.rendered_at = stamp
        super().do_GET()

    def log_message(self, fmt, *args):
        if not self.path.startswith("/assets/"):
            super().log_message(fmt, *args)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--site", default=os.path.join(render.ROOT, "dev", "fixture"))
    p.add_argument("--port", type=int, default=8642)
    args = p.parse_args()
    out = os.path.join(render.ROOT, "dev", "out")
    Handler.site, Handler.out = os.path.abspath(args.site), out
    os.makedirs(out, exist_ok=True)
    handler = functools.partial(Handler, directory=out)
    print(f"http://localhost:{args.port}/  (site: {args.site})")
    http.server.ThreadingHTTPServer(("127.0.0.1", args.port), handler).serve_forever()


if __name__ == "__main__":
    main()
