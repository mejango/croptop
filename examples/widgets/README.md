# Example widgets

The six example posts published on [follo.eth.sucks](https://follo.eth.sucks)
(also on eth.limo and eth.shop). Each `.html` file is a post's content; the
matching `.js` file is the attachment it references. Copy a pair into a post
of your own: paste the HTML as the content, drop the script on the editor.

| post | files | shows |
|------|-------|-------|
| The clock every campaign runs against | `countdown.html` | an inline module, no attachment |
| What Ethereum is doing right now | `gas.html`, `gas.js` | live RPC reads with the template's `ethers`, a canvas sparkline |
| Spend it your way | `budget.html`, `budget.js` | interactive sliders, state in `localStorage` keyed by `croptop.postId` |
| Money in motion | `motion.html`, `motion.js` | generative canvas seeded by the post id, reads the site's highlight color |
| Who is reading this | `wallet.html`, `wallet.js` | `croptop.wallet.connect`, read-only balance and ENS lookup |
| This site, so far | `timeline.html` | `croptop.site()` and `croptop.prefix` to draw every post as a timeline |
