# Collaborative and composite sites

A shared site remains a site, with its own logo, title and publishing key. The native sidebar adds a subtle people symbol. The header opens contributors and review; post cards show authors. Site settings has a Contributors tab.

## First version

The owner adds a contributor by ENS or IPNS address and chooses submitted posts or all source posts. Both modes require review. Publishing the shared site makes its contributor list available; Copy invitation produces instructions to share. No invitation is sent by the application.

A contributor uses Submit to shared site in the editor or a post’s context menu. The editor saves first. Publish and submit adds a destination marker and publishes the contributor’s site, including other saved changes. The sheet explicitly explains that the post is public on the source site while awaiting review. The destination checks its invited sources when the owner clicks Check for posts.

Incoming posts stay in private local staging. Read post shows text and declared images without executing source HTML or JavaScript. Add to site copies the reviewed snapshot and attachments; Dismiss is reversible. Only the owner’s normal Publish action updates the shared site. Accepted copies are not silently replaced when the source changes. Removing a contributor stops future inclusion and review actions but retains accepted posts.

Private drafts, automatic inclusion, shared editing and delegated publishing permissions are not part of this version. Contributor identities are sites, not new user accounts.

## Storage and compatibility

`contributors` contains IPNS, display name and mode (`submissions` or `all`). Planet’s existing `aggregation` sources are read as curated sources. Editing contributors migrates that list without deleting posts. Legacy originalSiteName, originalSiteDomain, originalPostID and originalPostDate survive store/public render/adoption. Existing imports are deduplicated by origin. An author’s source site owns submissionTargets; destination metadata never grants access to its publishing key.

Queue decisions and staged media live in the site’s collaboration directory, outside Articles and Public. Source fetches use the exact CID resolved from IPNS through the IPFS engine; HTTP fallback is not used to establish source content. Nested original-author metadata remains source-provided attribution, not a new cryptographic identity claim. Approval uses a stable per-source/post identifier, making retries idempotent and preserving owner edits. Review endpoints retain the local console’s authentication. Attachment traversal, symlinks, oversized files and undeclared preview files are rejected.

Published sites retain bylines in cards and post pages. Existing background engines must be updated as well as the native app to expose the new API.

## Verification

Go coverage exercises invitations, submission gating, owner approval, public attribution, media copying, persistent decisions, retry idempotency, revocation, legacy composites, offline sources, invalid paths and review authentication. Native review uses disposable fixture sites; no real site is published by tests.
