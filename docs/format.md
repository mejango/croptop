# Croptop site format, version 1

A Croptop site is an ed25519 key and a published directory. The directory
is what IPFS serves and what the template's JavaScript reads. It is also
enough to rebuild the editable source, so a site can move between machines
with nothing but its key. This document is the contract every client must
keep. The Planet Mac app writes the same files.

## Names

- **IPNS name**: base36 CIDv1 of the ed25519 public key, `k51…`. The key
  is stored in kubo's keystore format: bytes `08 01 12 40` followed by the
  64-byte seed-and-public-key, in a file named `key_` plus lowercase
  unpadded base32 of the key name. The key name is the site's UUID.
- **Site UUID**: uppercase RFC 4122 v4, like
  `FF5F456D-904F-4EE6-8BB5-AD175C65319A`. Post UUIDs have the same form.
- **Public URL**: `https://<domain>.<gateway>/` for an `.eth` domain, else
  `https://<ipns>.eth.<gateway>/`. Gateways: `sucks`, `shop`, `limo`.

## Timestamps

Every timestamp is a JSON number of seconds since 2001-01-01T00:00:00Z
(Apple's reference date). Unix time is that value plus 978307200. The
template adds the offset itself. Exception: `nft.json`'s `created_at` is
Unix seconds as a string.

## Published directory

```
planet.json               site fields plus "articles": [ article … ]
templateSettings.json     template settings the page reads into `env`
avatar.png, favicon.ico   optional
index.html, page1.html    rendered feed
<tag>.html                one per tag when the template asks for tag pages
rss.xml                   RSS 2.0 feed of the articles
robots.txt                empty, or "User-agent: *\nDisallow: /" when doNotIndex
assets/                   copied from the template
<post-uuid>/              one per post (also copied to <slug>/ when a slug is set)
  index.html              the post page
  simple.html             minimal post page for embedding
  article.json            the article object
  article.md              title, blank line, content
  nft.json                NFT metadata when the post has at least one attachment CID
  nft.json.cid.txt        CIDv0 of the exact bytes of nft.json
  <attachments…>          as uploaded
  _cover.png              generated 512x512 cover for text-only and audio posts
  _videoThumbnail.png     first frame of the video, when available
```

### planet.json keys

`id`, `name`, `about`, `ipns`, `created`, `updated`, `articles`,
`plausibleEnabled`, `plausibleDomain`, `plausibleAPIServer`,
`juiceboxEnabled`, `juiceboxProjectID`, `juiceboxProjectIDGoerli`,
`acceptsDonation`, `acceptsDonationMessage`, `acceptsDonationETHAddress`,
`twitterUsername`, `githubUsername`, `telegramUsername`, `mastodonUsername`,
`discordLink`, `farcasterEnabled`, `podcastCategories`, `podcastLanguage`,
`podcastExplicit`, `tags`. Absent optionals are omitted, not null.

`articles` holds every post with `articleType` 0 (blog), newest pinned
first, then newest created first. Pages (`articleType` 1) are rendered but
not listed.

### article object (article.json and each entry of articles)

`articleType`, `id`, `link` (`/<uuid>/` or `/<slug>/`), `slug` (may be
empty), `externalLink` (may be empty), `title`, `content` (markdown, may
contain HTML), `contentRendered` (HTML), `created`, `modified`, `hasVideo`,
`videoFilename`, `hasAudio`, `audioFilename`, `attachments` (file names),
`heroImage` and `heroImageURL` (absolute URL of the hero image),
`heroImageFilename`, `heroImageWidth`, `heroImageHeight`, `cids`
(attachment name to CIDv0), `tags` (name to name), `pinned` (timestamp when
pinned).

The hero image is `heroImage` when the author chose one, else
`_videoThumbnail.png` for video posts, else the first attachment whose name
ends in avif, jpeg, jpg, png, gif, or webp.

### nft.json

```
{
  "attributes" : [
    {"trait_type" : "title", "value" : <title>},
    {"trait_type" : "title_sha256", "value" : <sha256 hex of title>},
    {"trait_type" : "content_sha256", "value" : <sha256 hex of content>},   only when content is non-empty
    {"trait_type" : "created_at", "value" : <unix seconds as string>}
  ],
  "animation_url" : "https://ipfs.io/ipfs/<cid of video or audio>",         only for video or audio posts
  "description" : <summary, usually "">,
  "external_url" : <externalLink, else the post's public URL>,
  "image" : "https://ipfs.io/ipfs/<cid>",
  "mimeType" : <mime type of the first attachment>,
  "name" : <title>
}
```

`image` is the CID of the first attachment, of `_videoThumbnail.png` for
video posts, or of `_cover.png` for audio posts.

The bytes matter: `nft.json.cid.txt` is the CIDv0 (`ipfs add --only-hash
--cid-version=0`) of the file, and onchain tiers are keyed by it. The file
is written the way Swift's JSONEncoder does with `.prettyPrinted`,
`.sortedKeys`, `.withoutEscapingSlashes`: two-space indent, `"key" : value`
with spaces around the colon, empty containers as `[` newline newline
indent `]`, no trailing newline. Once written, the file is only rewritten
when the post's title, content, or attachments change.

## Source directory (what an editor keeps)

```
sites/<uuid>/planet.json              same keys as the published one minus articles, plus
                                       domain, templateName, lastPublished, lastPublishedCID,
                                       archived, customCode*, pinning settings, ipnsSequence,
                                       publishedElsewhere, croptopGateway
sites/<uuid>/templateSettings.json
sites/<uuid>/avatar.png, favicon.ico
sites/<uuid>/Articles/<post>.json     article source: id, title, content, created, modified,
                                       articleType, link, slug, summary, attachments, cids,
                                       tags, heroImage (file name), heroImageWidth/Height,
                                       videoFilename, audioFilename, externalLink, pinned,
                                       isIncludedInNavigation, navigationWeight
sites/<uuid>/Articles/<post>/<files>  attachments and generated images
```

Unknown keys must be preserved when a file is rewritten.

## Rebuilding source from a published directory

1. `planet.json` minus `articles` becomes the site file; set `templateName`
   to `Croptop`.
2. Each `<post>/article.json` becomes the article source. Take `content`
   from it, not from `article.md`. Set `summary` to an empty string.
   `heroImage` is kept only when it differs from the automatic choice.
3. Copy attachments, `_cover.png`, and `_videoThumbnail.png` into the
   post's source folder. Keep `nft.json` and `nft.json.cid.txt` as they are.
4. Read the current IPNS record to learn the sequence number before
   publishing again.

## IPNS publishing

Records are published with lifetime 7200h and TTL 1m. Every publish reads
the network's current record and uses `max(local, network) + 1` as the
sequence. If the network's record points somewhere this client did not
publish, the client stops and asks for a sync.

## Template context (for template authors)

Templates are Django-syntax HTML (Stencil in Planet, pongo2 here) under
`templates/`. `index.html` renders the feed and tag pages, `blog.html` a
post, `simple.html` a minimal post page. Every render receives:

| key | value |
|-----|-------|
| `planet` | the site: `name`, `about`, `ipns`, `tags` (list of tag names), social usernames, `plausible*` |
| `planet_ipns` | the IPNS name |
| `articles` | list of article objects (feed order) |
| `article` | the current post (post pages) with `created.timeIntervalSince1970` |
| `article_id`, `article_type`, `article_title`, `article_summary` | post pages |
| `content_html` | the post's markdown rendered to HTML |
| `page_title`, `page_description`, `page_description_html` | for `<head>` |
| `site_navigation` | list of `{id, title, slug, externalLink, weight}` for posts marked "show in navigation" |
| `has_avatar`, `has_podcast` | booleans |
| `og_image_url`, `social_image_url` | absolute image URLs for sharing |
| `assets_prefix` | `./` on the feed, `../` on post pages |
| `template_settings` | `template.json` settings definitions |
| `user_settings` | the site's chosen values, defaults filled in |
| `custom_code_head`, `custom_code_body_start`, `custom_code_body_end` | owner-provided HTML |
| `build_timestamp` | Unix seconds, for cache busting |
| `style_css_sha256` | hash of `assets/style.css` |
| `current_item_type` | `index`, `blog`, or `tags` |
| `tag_key`, `tag_value` | on tag pages |

Filters: `escape`, `mdyydot` (`1.2.06`), `formatDateC` (RFC 3339 local),
`md2html`, `rfc822`, `hhmmss`. `x.count > 0`, `x != nil`, `x == true`, and
`dict['key']` from Stencil are rewritten before parsing.

`template.json` fields: `name`, `version`, `buildNumber`,
`generateNFTMetadata`, `generateTagPages`, `settings` (name, type,
defaultValue, description, advanced). Settings reach the page through
`templateSettings.json`, which the template's JavaScript fetches into `env`.
