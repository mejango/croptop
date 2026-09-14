# Croptop welcome posts

Eleven editable posts for croptop.eth. `manifest.json` maps their stable post IDs and display order.

`build.py` generates each `posts/<name>/post.html` and `preview.js`. `welcome.js` is a classic script so it can mount again when a feed modal reopens. CSS is scoped to `.ct-welcome`. Every post attaches its HTML, the script, and a feed preview. The playgrounds have no dependencies. Embeds reads a remote manifest and loads the approved external website; the other examples run locally.

The Custom post is a playable eight-step rhythm machine; the Embeds post loads Juicebox when its manifest and framing policy permit Croptop; the network is explicitly an illustration. Motion respects reduced-motion preferences and hidden tabs.

Publishing data lives in `~/Library/Application Support/croptop/sites/DFF00C4E-05B8-4DD3-8CF4-95DF628F4C95/`. The tagline and existing post URLs are preserved. Original media remains on disk, even where it is no longer the post cover.

`site-head.html` is the site’s custom head: it makes feed cards fill the width on mobile, overriding the template’s desktop percentage widths.

Preview code is embedded in each post so it updates with the content, avoiding stale cached `preview.js` modules. The downloadable preview remains attached. The playground script URL includes its content hash.

Each post ends with one Source download. `source.html` includes the post markup, scoped styles, and playground JavaScript in a standalone HTML page (external embeds require a network connection). Preview controls adjust layout, rhythm and tempo, garden generation, and peer count without opening the post. FAQ answers remain visible.

`drawing.js` draws the original ink illustrations in Canvas. The build inlines it into previews and the playground script. Writing, audio, and video posts demonstrate ordinary media; the Custom and Embeds posts demonstrate programmable content. Media samples are original; the video frames are rendered with the same JavaScript drawing code.

`rhythm.js` provides the shared Web Audio sequencer for the Custom preview and full post: 40–200 BPM, paired-step swing, and three tracks with drum, percussion, and melodic instrument choices. Start has an open-door illustration. Bots uses the expressive flowers with the original petal slider and offers seven copyable journey prompts through `bots.js`. Embeds uses the site illustration; its full post checks the remote opt-in before replacing a link preview with a live iframe. `download/build.py` builds the separate Get Croptop download cards. `branding/scissors.png` is the original Croptop site logo.

`content/essay.html` and `content/technical.html` hold the full writing samples. `content/embeds.html` holds the external website example.

`embeds.js` reads the experimental Croptop entry in a remote site’s root manifest. The Embeds post uses Juicebox and keeps an open-site link when embedding is unavailable. See [embed-doc.md](embed-doc.md) for the opt-in format and browser policy. The Basics post also offers a copiable troubleshooting prompt for your bot.
