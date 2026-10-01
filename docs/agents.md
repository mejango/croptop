# Posting to a croptop site from an agent

An agent, bot or CI job can post to a croptop site with nothing but the site's
key. It needs no copy of the site, no gateway, and nothing kept between runs.

## Setup (the site's owner, once)

1. Publish the site once from the Croptop app or `croptop serve`. This puts
   it on its host, crop.top unless you set another.
2. Export the site's key and give it to the agent as a secret:

   ```
   croptop key export <site> > site.pem
   ```

   The key controls the site. Keep it in the agent's secret store, never in
   a repository.

## Each run

1. Install the latest croptop. Repeating this is safe.

   ```
   curl -fsSL https://crop.top/install.sh | sh     # macOS and Linux
   irm https://crop.top/install.ps1 | iex          # Windows
   ```

2. Post:

   ```
   croptop post --key site.pem --title "Title" --content "Body in **markdown**" [--tags a,b] [files…]
   ```

   Files are images, videos or audio to attach. When the command finishes, it
   prints the post's URL, its CID and the site's new sequence. The run can end
   right after: the host keeps the post online.

## Rules

- Never print, log or echo the key.
- If the command fails with "host already has sequence" or "host holds …, not
  …", the site changed while you were posting. Run the same command again.
- If it fails with "the network has a newer version of this site", the owner
  published from a machine that didn't reach the host. Tell the owner; posting
  again won't fix it.
- Some owners publish to their own host instead of crop.top. For those sites,
  add `--host https://<their host>`. The agent and the owner's app must use the
  same host.
- Use croptop 0.13.17 or newer. The install line always gets the latest.
  Older versions depend on public IPFS services that shut down on 2026-09-30.
