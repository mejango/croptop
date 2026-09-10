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
| hot-wash | #FFF0FA | background of the current rail item |
| live | #3BB273 | node up, published |
| attention | #EFAB1D | update available, unpublished changes |
| rule | #E2E2E2 | quiet borders, dividers |
| muted | #ADADAF | secondary text, placeholders |

No dark mode yet. The apps follow the console: paper stays white.

## Type

| role | face | size | weight | notes |
|---|---|---|---|---|
| brand | Capsules | 22 | 500 | "Croptop" in the rail |
| h1 | Capsules | 28 | 500 | screen titles, letter-spacing 0.01em |
| h2 | Capsules | 18 | 500 | section titles |
| rail heading | Capsules | 12 | 500 | muted, "Sites", "Following" |
| button | Capsules | 14 | 500 | all bordered buttons |
| body | Simplon | 16 / 1.45 | 400 | everything else |
| small | Simplon | 13 | 400 | second lines, dates, help; muted |
| code | system monospace | 14 | 400 | markdown editor, keys |

Fonts ship with the apps as TTF (`installer/fonts/`): CapsulesVF (variable,
wght 100 to 700), SimplonNorm Regular and Bold.

## Layout

| token | value |
|---|---|
| rail width | 220 |
| rail padding | 20 top/bottom, 16 sides |
| content padding | 28 |
| gap scale | 4, 8, 12, 16, 22 |
| border | 2 px ink; 2 px rule for quiet |
| radius | 0 everywhere |
| focus ring | 3 px hot, offset 2 |
| tile | square, 2 px ink border, hero cover-fit, title below |
| sheet (form) | max width 720, labels above fields, 12 gap |
| select | sized to its longest option, chevron in its own 36 px, 12 px from the edge |

## Components

- **Button**: 2 px ink border, paper fill, Capsules 14, padding 8 x 14. `hot`
  variant: hot border and text. `quiet` variant: rule border, Simplon.
- **Rail item**: name (body), second line (small, muted), 4 px left bar hot
  when current, hot-wash background.
- **Card (feed)**: who line (avatar 20, name, small date muted), title (h2),
  excerpt (body, 3 lines), media below, 2 px rule border, 16 padding.
- **Toast**: bottom center, ink on paper with 2 px ink border, 3 s; error
  variant hot border.
- **Update banner**: attention background, ink text, "Update to x.y.z" button
  and "Later".
