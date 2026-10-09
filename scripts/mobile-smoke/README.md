# Isolated mobile production smoke

This harness creates a **new empty synthetic test site**. It never reads the
desktop library, uses a user's site, or imports a global keystore. The node has
no P2P listeners and stops before the bootstrap exits. The phone then publishes
using only the deployed service, proving that the desktop need not stay awake.

Preparation and tests do not publish anything:

```sh
go test ./scripts/mobile-smoke/bootstrap
node --test scripts/mobile-smoke/browser.test.mjs
go run ./scripts/mobile-smoke/bootstrap --dir /private/tmp/croptop-mobile-production-smoke
```

The last command prints only public identity, CID, and local file paths. The
generated `site-key.pem` is mode `0600` inside a `0700` directory. Do not print
the key, commit the task directory, upload the browser profile, or attach a
network trace containing authorization headers.

## Explicit publication checkpoint

Only run the next command when publication of this dedicated test site to
`crop.top` is authorized. Both flags are mandatory. A lost response is
reconciled against the signed head on rerun; an advanced head is never replaced
with the initial empty fixture.

```sh
go run ./scripts/mobile-smoke/bootstrap \
  --dir /private/tmp/croptop-mobile-production-smoke \
  --publish --host https://crop.top
```

Configure a local Playwright installation without adding dependencies to the
repository. For example, use `PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs`
and optionally `CHROMIUM_EXECUTABLE=/absolute/path/to/chrome-headless-shell`.

Read-only service preflight:

```sh
node scripts/mobile-smoke/browser.mjs \
  --dir /private/tmp/croptop-mobile-production-smoke \
  --origin https://croptop-phone-923c1bafd14ea328.croptop.workers.dev
```

Add `--publish` to explicitly authorize connection consent, preparation,
review, and one synthetic post through the real mobile UI:

```sh
node scripts/mobile-smoke/browser.mjs \
  --dir /private/tmp/croptop-mobile-production-smoke \
  --origin https://croptop-phone-923c1bafd14ea328.croptop.workers.dev \
  --publish
```

Rerun that exact command to recover or verify the **same** post. The persisted
browser profile and `browser-run.json` bind the site, origin, browser engine,
and operation identity. Missing retained browser data causes a refusal, not a
fresh post. A separate test requires a new fixture directory/site. Do not reuse
this fixture to test a second browser engine; `--browser webkit` needs its own
fixture.

The smoke blocks outbound requests containing PEM, DER, or common encoded
forms of the private key. Every actual browser request is inspected; service
workers are disabled specifically for complete request visibility. Existing
browser tests separately cover the offline service-worker shell.

Success requires: a non-exportable stored browser key; explicit hosting-service
consent; reviewed normalized preview; reload/status recovery; exactly one public
article with the retained identity; public image bytes matching the prepared
SHA-256; and a working public receipt page. Screenshots and a nonsecret JSON
receipt stay in the task directory. The fixture site remains public: cleanup is
a separate, explicitly authorized action, not an automatic destructive step.
