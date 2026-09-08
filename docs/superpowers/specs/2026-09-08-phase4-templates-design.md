# Phase 4: Templates

Date: 2026-09-08. Parent: 2026-09-08-croptop-roadmap.md.

## Goal

A beginner never thinks about templates. A curious owner clicks *Fork*
and edits one CSS rule with the rendered site updating beside it. A
template author publishes a template to IPFS and anyone installs it by CID
or ENS name. Templates stop being baked into the binary.

## Where templates live

Three sources, resolved in this order for a site:

1. `sites/<id>/template/` when the site forked its template (per-site).
2. `<data>/templates/<cid>/` for an installed template (shared).
3. The embedded default (the Croptop submodule), used when the site names
   none.

Site JSON gains `croptopTemplate`: `{ "cid": "<template root cid>",
"upstream": "<ens or ipns name, optional>", "forked": true|false }`.
Planet ignores it. `templateName` stays `Croptop` for Planet compatibility.

A template is a directory with `template.json`, `templates/`, and
`assets/`, exactly the layout of SiteTemplateCroptop. Publishing one is
`croptop template publish <dir>`: add to IPFS, provide, print the CID.
Installing one is `croptop template install <cid | ens>` or the browser in
the console: fetch (same IPFS-then-gateway path), validate that
`template.json` and `templates/index.html` exist and render the fixture
site without error, store under `templates/<cid>/`.

## Fork and edit

*Fork* copies the site's current template directory into
`sites/<id>/template/` and sets `forked: true`. The console's *Template*
tab lists the files, opens one in a code editor (a `<textarea>` with a
monospace face and line numbers is enough; CodeMirror is not needed), and
renders the site preview in an iframe on every save. Saves go through
`PUT /v0/croptop/sites/{id}/template/file?path=…`. Publish uses the forked
template like any other. *Reset to upstream* deletes the fork.

*Update from upstream*: when `upstream` resolves to a newer CID, the
template tab offers a three-way view: the forked file, the old upstream,
the new upstream, with a per-file *take theirs* or *keep mine*. Full merge
tooling is out of scope; per-file choice covers the common case of
touching `style.css` only.

## Template browser

`GET /v0/croptop/templates` lists installed templates with name, version,
CID, and which sites use them. *Browse* accepts a CID or ENS name. The
default entry is the embedded Croptop template with its build number. A
site's settings gain a *Template* selector.

## Renderer changes

`render.Renderer` gains `TemplateFor(site) (fs.FS, error)` that implements
the three-source resolution and caches parsed template sets by directory.
The `{{ }}` context is unchanged, and `docs/format.md` gains a section
documenting every context key so template authors have a reference.

## Tests

- Template resolution order with a forked site, an installed template, and
  the default.
- Fork copies, edit renders, reset restores.
- Install validates and rejects a directory without `templates/index.html`.
- Publish then install round-trips a template through the offline embedded node.

## Out of scope

Live collaborative editing, template marketplace payments, migrating sites
between templates with different settings keys.
