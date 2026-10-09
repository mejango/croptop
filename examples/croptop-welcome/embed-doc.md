# Croptop embeds (experimental, version 1)

A site can opt in through its public `/manifest.json`. This follows Safe's manifest discovery pattern and can coexist with the existing Safe fields:

```json
{
  "name": "Juicebox",
  "croptop": {
    "version": 1,
    "embed": "/",
    "origins": ["https://crop.top", "https://croptop.eth.sucks"]
  }
}
```

Serve the manifest with `Access-Control-Allow-Origin: *`. Croptop fetches it without credentials. The `embed` URL must use HTTPS and resolve to the manifest site's origin. `origins` contains exact parent origins; version 1 does not interpret wildcards. A missing or unsupported entry leaves a link preview. This is a proposed Croptop convention, not an existing web or Safe standard.

The manifest describes consent; the browser enforces it through the embedded page's HTTP response headers. Add the same parent origins to its `Content-Security-Policy: frame-ancestors` directive, preserving existing entries. Juicebox's prototype permits:

```
frame-ancestors https://app.safe.global https://app.5afe.dev https://plugin.money https://www.plugin.money https://crop.top https://croptop.eth.sucks
```

A conflicting `X-Frame-Options` header must not be sent. A meta tag or CORS header cannot override framing restrictions. Check reverse-proxy/CDN headers as well as app configuration.

Croptop sites can have arbitrary domains. A browser cannot recognize the software behind a domain, so approving these two sites does not approve every Croptop site. Add another origin deliberately to both the manifest and the header. IPFS gateway and CID subdomains are distinct origins. The local Croptop app deliberately sandboxes post content with an opaque origin; it keeps the link fallback rather than weakening that isolation.

The example post reads the manifest on opening, with an eight-second timeout, and inserts a cross-origin sandboxed iframe only for an approved origin. An “Open site” link remains available. A manifest is not proof that the final framing headers permit the page: deployment settings or redirects can still block it. Cross-origin iframe load events cannot reliably distinguish that failure. Keep the manifest and HTTP policy together and test in a browser.

No wallet provider or Safe SDK bridge is exposed to the embedded app. It retains its own connection flow. Scripts, forms, downloads and user-opened tabs are allowed; top-level navigation and access to the parent document are not. Wallet signing was not part of this trial.

## Validation

- Juicebox: `node node_modules/vitest/vitest.mjs run test/safe-app-config.test.ts` checks the exact framing allowlist, public manifest/CORS and existing Safe icon/permissions.
- Croptop example: `node --test test/embeds.test.mjs` checks version handling, exact origins, opaque origins, invalid URLs and same-origin restrictions.
- Browser trial: a Croptop-origin fixture frames the actual local Juicebox development server with its response CSP preserved. Missing, invalid, unavailable and unapproved manifests retain the link. This is a local trial; the production Juicebox server needs these changes deployed before the public embed can load.
