# Croptop design tokens

The single source of truth for the three native apps and the web console.
Change a value here, then change it in `web/style.css`, `apps/macos`,
`apps/windows` and `apps/linux` in the same commit.

## Color

| token | hex | use |
|---|---|---|
| ink | #171717 | text, borders, rules (2 px) |
| paper | #FFFFFF | background |
| hot | #F056C1 | the one accent: current item bar, focus ring, primary button fill on hover |
| hot-wash | #FFF0FA | drag-and-drop target wash |
| live | #3BB273 | node up, published |
| attention | #EFAB1D | unpublished changes |
| rule | #E2E2E2 | quiet borders, dividers |
| muted | #ADADAF | secondary text, placeholders |

No dark mode yet. The apps follow the console: paper stays white.

## Type

| role | face | size | weight | notes |
|---|---|---|---|---|
| brand | Simplon | 22 | 700 | "Croptop" in the rail |
| h1 | Simplon | 28 | 700 | screen and sheet titles |
| form section/label | Simplon | 14 | 700 | field titles, disclosures, and numbered setup titles |
| form value | Simplon | 14 | 400 | input values and instructions in all forms and settings |
| rail heading | Simplon | 16 | 700 | "Your sites", "Following" |
| button | Simplon | 14 | 400 | actions and editor mode controls, in every state |
| body | Simplon | 16 / 1.45 | 400 | everything else |
| small | Simplon | 13 | 400 | second lines, dates, help; muted |
| code | system monospace | 14 | 400 | editable Markdown/HTML and copyable code |

The app bundles SimplonNorm Regular and Bold from `installer/fonts/`. Register
both faces before SwiftUI constructs the first scene so initial headings use the
real bold font. All headings and ordinary text use this family.

ENS and DNS setup use matching disclosures with three numbered steps. Each step
has a bold 14 pt title followed by its instructions and actions. Environment menus
place the chevron after the selected Production or Testnets label.

## Layout

| token | value |
|---|---|
| rail width | 220 |
| rail padding | native: 4 top, 8 bottom, 16 sides; web: 20 top/bottom, 16 sides |
| content padding | 28 |
| gap scale | 4, 6, 8, 12, 16, 22, 24 |
| border | fields: 1 pt rule, muted on focus; bordered buttons: 2 px ink or rule |
| radius | 0 everywhere |
| focus ring | 3 px hot, offset 2 |
| post views | Tiles: natural-proportion masonry; More: compact square grid; List: thumbnail rows |
| sheet (form) | max width 720, labels above fields, 24 group gap; 8 label/control; 6 helper |
| select | sized to its longest option, chevron in its own 36 px, 12 px from the edge |

## Components

- **Button**: 2 px ink border, paper fill, Simplon 14, padding 8 x 14. `hot`
  variant: hot border and text. `quiet` variant: rule border. All variants and
  selected states keep the same Simplon font.
- **Secondary actions**: Website, Settings, Cancel and Save show only their icon,
  with the action name in a hover tooltip and a complete accessible label. Keep
  a stable 28 × 36 pt target; hovering does not change its width or reflow nearby
  text. Website uses the globe icon. Quick post uses the same treatment for Discard / Post.
- **Primary actions**: Publish, Save & publish and Post and publish remain visible
  at the right. Secondary icon actions use 8 pt gaps; leave 16 pt before the
  bordered primary button. Save & publish uses a text-only label and the site’s highlight color.
- **Editor actions**: Cancel uses an x mark and Save uses a checkmark. Delete post
  sits at the bottom of the form in a separate Danger zone. Expand preview is
  plain text with an expand-arrow glyph. Write / Split / Preview use Simplon.
- **Fields and editors**: white fill, 1 pt inset rule border, muted on focus.
  Use this treatment throughout settings, posting, tags, and creation/follow forms.
  Labels and values use Simplon 14; helpers use muted Simplon 13. Compact controls
  reduce padding while keeping the same type size; addresses match other values.
- **Rail item**: name (body), second line (small, muted), faint pink background and ink text when current, without a left rule. The highlight extends to the window’s left edge; text stays aligned with the logo at 16 pt. Keyboard focus adds a subtle bottom rule, without an outline.
  Drag anywhere on an owned site row to reorder it. Native insertion-gap feedback
  and edge autoscrolling make the destination clear. Save order only on a valid
  drop; Escape or dropping outside leaves it unchanged. The context menu provides
  Website, Settings, and Publish, and order is remembered locally. Move existing
  rows over 0.18 s after a drop without reloading unchanged cards; Reduce Motion
  moves them immediately.
- **Card (feed)**: who line (avatar 20, name, small date muted), title (h2),
  excerpt (body, 3 lines), media below, 2 px rule border, 16 padding.
- **Toast**: bottom center, ink on paper with 2 px ink border, 3 s; error
  variant hot border.
- **App update indicator**: a small 11 pt “Update available” text button beside
  the version at the bottom of the rail. Deferred-relaunch guidance sits below that row. No large banner in the native app. Sparkle compares signed feed builds with the installed app build, independent
  of a separately running engine. Click to download, install, and relaunch;
  open editors and active publishes defer relaunch. Croptop → Check for Updates…
  is always available when an update check can start. The Mac rail shows the
  app version; its tooltip identifies the build and connected engine version.

## App icon

Use the original black scissors on white artwork in `installer/Croptop.icns`.
Do not substitute a letter C. `installer/icon.png` is its PNG export from macOS iconutil.
`go run ./installer/icon` wraps that exact PNG in `installer/Croptop.ico`
for Windows packaging.

## Editing

- Tags are added and selected in post editors only; Site settings does not edit tags.
- Tags use compact square chips with a rule-colored background and no border,
  Simplon 13, and 4 × 8 pt padding. Inactive text is #555; selected text is bold
  ink with a trailing × to remove the tag. The all filter has no ×. Chips use
  4 pt gaps; editor chips wrap. Keep an Add a tag field beside an Add action.
- Attachment thumbnails use a 160 x 120 contain-fit area. Clicking an image or
  Preview opens a large viewer with zoom, Fit and Done controls.

- Feed uses the same plain sidebar row and selected treatment as sites, without an outlined button.
- The native rail uses the original square black-scissors artwork (`Resources/Scissors.png`)
  and 4 pt top padding. Selected rail rows use a faint pink background; selected
  bordered editor controls use ink fill with paper text.
- Site links stay on one line, truncate in the middle, and show the full address
  on hover. Page type is a quiet document icon beside the date, never over media.
- New site keeps Name, About and an optional Logo visible in both modes, with
  logo preview and removal before creating. Compact horizontal Start fresh / Curate radio options
  sit below Logo. Curate reveals a Sites field with a brief explanation of combining
  posts while crediting their original authors, and one ENS name or IPNS address per
  line. Switching modes preserves every draft field; Start fresh ignores saved
  source entries when creating. Curate requires at least one nonblank source.

- Place Include in navigation and Pin to the top directly below the title field.
  Give title/options, body, attachments, and tags 24 pt separation, with smaller
  12–16 pt gaps within each section. Leave 16 pt before the Add a tag row.
- Post editing offers Write, Split and Preview modes, a 460 pt preview area, and
  Expand preview for a larger view. Render Markdown/HTML with responsive media.
  Preview scripts/forms stay disabled and selected local files are scoped by name.
- Site settings group Site, Domain, Money and Advanced controls. Save keeps
  changes local; Save and publish explicitly publishes them. Load errors offer Retry.

- Address claims show inline progress, confirmed address on success, and an inline error on failure.
- ENS setup sits directly beneath the ENS name and offers a copy-and-open handoff, three numbered steps, and the visible site address. It does not imply a transaction was submitted.
- Announcement and accent color live in Site; search indexing lives in Advanced, with help directly beneath the checkbox. Payments uses compact fields capped at 440 pt, with inset strokes for even borders. Disclosure labels toggle their entire row.
- Site settings uses text tabs with an active underline, icon save/cancel actions with hover tooltips and a highlighted Save & publish action, and a separate Danger zone for site removal. Publishing-key backup lives inside Danger zone, with clear copy that anyone holding the key can post to the site and should keep it private. The default website gateway is crop.top; explicit saved choices remain respected.
- Advanced includes Analytics (site domain and server; a nonempty domain enables Plausible) after Hosting and Custom website code. Analytics saves are verified against the connected service so an older service cannot silently discard them.

The rail scissors are shown without the icon’s square canvas, 64 pt wide, aligned
with the 16 pt left edge shared by Feed, site names, and section headings.
The selected row’s background extends behind that margin to the window edge.

Money uses compact inline label/value rows: 14 pt bold labels, 14 pt regular
values, and a 1 pt pale rule. ENS name and host inputs, and the free-address input/Claim row, are also capped
at 440 pt. Site address appears first in Domain settings, followed by the free site name and ENS setup.
Social profile fields are omitted from the native settings UI.

The site header’s external link is labeled “Website”; its tooltip retains the full URL.
Leave 20 pt between Settings and Publish so the publishing action has room.

Owned site rows include a 56 pt square logo flush to the window edge, filling
the row height. Images sit on opaque paper above the selection background,
without tint or rounded corners. A missing logo uses the site’s initial.

Post view controls sit to the right of the scrollable tags: a 2 × 2 block icon
for Tiles, a 3 × 3 dot icon for More, and three horizontal lines for List,
separated by thin vertical rules. The selected icon uses ink, others muted;
all have tooltips and keyboard access. Remember the selected view locally.

Tiles use the template's natural image proportions, up to three equal-width
columns with 20 pt gaps. Posts are assigned left to right and each column stacks
independently. Columns adapt to a 260 pt minimum tile width. Preserve the complete
image, reserve its metadata dimensions when available, and use the loaded image's
size otherwise. Borders are 1 pt rule, changing to the site's accent on hover or
keyboard focus. The New post tile is 4:3.

More uses up to five columns of square cover crops, 16 pt gaps and a 150 pt
minimum width. Its New post tile is also square. Title, date, page indicator,
author and tags sit on a bottom scrim (ink at 78% opacity). Titles use white
Simplon Bold 14, metadata uses white at 85% opacity in Simplon 12. In Tiles this
caption appears on hover or keyboard focus with a 0.2 s fade; in More it stays
visible. Captions do not change card dimensions.

List uses 80 pt square thumbnails with title, author, date and tags alongside,
separated by 1 pt rules. All three views retain post actions and tag filtering.

Site rows have zero vertical spacing so their square logos touch edge to edge.

The divider between the sidebar and content is a 1 pt pale rule.

Following rows use the same full-height, square logos and fallback initials as
owned sites, loaded from each followed site’s cached avatar.

Following appears above Your sites, with separate scrolling lists. Section titles
use 18 pt Simplon Bold without underlines, with 12 pt below each heading row.
Both lists share the logo row style. Clicking the Following or Your sites title
opens that section’s combined feed; there are no separate All buttons. Followed
sites open in the app, with source attribution, older-post pagination, and a reader
for complete posts.

Combined feeds and followed-site feeds use the same Tiles / More / List picker,
remembered layout preference, column widths, spacing, tiles and rows as owned sites.
Tiles preserve image proportions; More uses square crops; List uses 80 pt
thumbnails. Source name/avatar and date identify each post in captions or rows.
The layout fills the content width, with the picker aligned to the right.

Peers align to the very top right beside the scissors. A muted 11 pt “Version X” label
sits at the bottom left, with 12 pt above and 8 pt below, retaining its build/engine
tooltip and an adjacent “Update available” text button when an update is confirmed.
Each section has a right-aligned +: Following opens the Follow form; Your sites
opens a New site form with Start fresh and Curate options. Scrollbars reach the right
edge of the column; text retains its inset.

Site headers show a 64 pt square site logo alongside the name and description,
using the same untinted image and missing-image fallback as the sidebar.

- Domain includes Website gateway. Publishing uses crop.top automatically; custom hosts live in Advanced → Hosting.
- Money separates the rewards wallet from shop addresses and explains each. Shop and Network connections each have a plain Production/Testnets dropdown that shows only the selected group while preserving all saved values.
- The standalone Curate sheet labels its name field “Curation name”; the combined New site form uses “Name”.

- Custom domain setup keeps three short steps: arrange Croptop hosting, point the domain to that host, then select it for publishing. Copy setup instructions provides the complete handoff; server commands and the optional DNSLink TXT record stay collapsed. The generated host command already selects the site with its IPNS root. Only a configured custom domain overrides Website links; entering one does not provision hosting, DNS, or HTTPS.

Form typography uses Simplon throughout settings, New post, Quick post, New site,
Follow and Curate: section headings use 16 pt Bold, labels and inputs use 14 pt,
and supporting text uses muted 13 pt. Keep 24 pt between groups, 8 pt from a
label to its control, and 6 pt before helper text. Screen and sheet titles use Simplon Bold; monospace is reserved for editable
Markdown/HTML and copyable code. Addresses use the same form text as other values. Curate opens with the
name field focused; its close button stays plain until reached by keyboard.

Shop category appears separately from network addresses. When the template exposes
that setting, it remains editable; older saved values appear as Automatic when the
current template chooses posting rules for the buyer. The saved value is preserved.

Plausible analytics uses a filled Site domain as its opt-in; clearing it disables
analytics. The analytics server field alone does not enable it. Hosting uses its
disclosure title as the field title. Shop defaults are explained once above the
network fields for the selected Production or Testnets environment.

Nested server setup and DNSLink disclosures use the same full-row toggle as
other settings disclosures. Followed-site headers offer Unfollow beside Refresh;
aggregate feeds do not. Unfollow returns to Following after the server confirms
removal and preserves the current screen if the user navigated elsewhere.

Node startup and post loading use the template’s standalone text ticker (-,
backslash, |, /), advancing every 100 ms in a fixed-size frame, with no visible
caption. The accessibility label describes the current operation. Reduce Motion
shows a static |. Startup failures retain their diagnostic message.
Empty sidebar sections and feeds say “Nothing yet.” without a call to action.
