---
name: pushpage-publish
description: Publish a folder of static files (HTML, CSS, JS, images) to the team's pushpage server and return a public HTTPS link. Use when the user asks to publish, deploy, share, upload or "put online" a landing page, site, prototype or HTML file built in this project, or asks for a link to it.
---

# Publish a static site with pushpage

pushpage hosts finished static files. It does not build projects: if the project needs a build step (`npm run build`, `vite build`, `astro build`, `next build && next export`), run it first and publish the output folder.

## 1. Check the CLI and the login

Run:

```bash
pushpage version && pushpage sites --json >/dev/null && echo READY
```

- `command not found`: the CLI is not installed. Stop and tell the user to open the **Connect AI** page of their pushpage web interface and follow the install instructions there. Do not try to install it yourself.
- An error that mentions `pushpage login` or `401`: there is no valid token. Stop and tell the user to issue a token on the **Connect AI** page and run the `pushpage login` command shown there in their own terminal. Never ask the user to paste a token into the chat.

## 2. Find the folder to publish

The folder must contain `index.html` at its root.

- Prefer the build output if the project has one: `dist/`, `build/`, `out/`, `public/` (for plain sites), `_site/`.
- A single HTML page: publish the folder that contains it; rename the file to `index.html` if it is the only page.
- Do not publish the project root if it contains source code, `node_modules`, `.env` files or secrets.

Hidden files and `.git`, `node_modules`, `.DS_Store` are skipped automatically, but check that nothing private is in the folder.

## 3. Publish

```bash
pushpage deploy <folder> --json
```

- The first run creates a new site and writes `.pushpage.json` into the folder. Later runs update the same site; keep that file.
- To publish to an existing site, add `--site <id-or-slug>` (list them with `pushpage sites`).
- `--title "<name>"` sets the site name on first publish.

The JSON output has `url` (primary address), `urls` (all addresses) and `deploy_id`.

## 4. Report the result

Tell the user the `url` as a clickable link, and mention that:

- the page is live now; repeating the publish updates it in place;
- `pushpage rollback <site>` restores the previous version if something broke;
- a memorable address (slug such as `promo-may`) can be set in the web interface.

## Errors

The CLI prints the server message. Common ones:

| Message | What to do |
| --- | --- |
| no `index.html` | Publish the folder that contains `index.html`, or rename the main page |
| archive too large, too many files, file too large | Remove unused assets (videos, source images) or compress them, then retry |
| symbolic link | Replace symlinks with real files |
| site limit reached | Ask the user to delete an old site or contact the administrator |
| too many requests | Wait the time the CLI shows, then retry once |
