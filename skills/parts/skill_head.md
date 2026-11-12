---
name: dropsite-publish
description: Publish a folder of static files (HTML, CSS, JS, images) to the team's dropsite server and return a public HTTPS link, build self-contained fast sites for it, and build contact or lead forms that work on dropsite without third-party form services. Use when the user asks to publish, deploy, share, upload or "put online" a landing page, site, prototype or HTML file built in this project, asks for a link to it, or wants a form (lead, contact, order, callback) on a site that will be published with dropsite.
---

<!-- Generated from skills/parts and skills/guide: edit those files and run `go test ./skills -update`. -->

# Publish a static site with dropsite

dropsite hosts finished static files. It does not build projects: if the project needs a build step (`npm run build`, `vite build`, `astro build`, `next build && next export`), run it first and publish the output folder.

Talk to the user in plain words: say what you did and give the link; explain commands or files only if they ask.

## 1. Check the CLI and the login

Run:

```bash
dropsite version && dropsite sites --json >/dev/null && echo READY
```

- `command not found`: the CLI is not installed. Stop and tell the user to open the **Connect AI** page of their dropsite web interface, press **Connect** and run the command shown there in their own terminal (it installs the CLI and signs in). Do not try to install it yourself.
- An error that mentions `not logged in`, `dropsite login` or `401`: the CLI is not signed in or its key was revoked. Stop and give the user the same advice: press **Connect** on the **Connect AI** page and run the shown command in their own terminal. Never ask the user to paste a token or a connect code into the chat.

## 2. Find the folder to publish

The folder must contain `index.html` at its root.

- Prefer the build output if the project has one: `dist/`, `build/`, `out/`, `public/` (for plain sites), `_site/`.
- A single HTML page: publish the folder that contains it; rename the file to `index.html` if it is the only page.
- Do not publish the project root if it contains source code, `node_modules`, `.env` files or secrets.

`.git`, `node_modules`, `.DS_Store` are skipped automatically; everything else in the folder becomes public, so check that nothing private is in it. If the folder holds secret-looking files (`.env*`, `.npmrc`, `.netrc`, `.git-credentials`, `.ssh/`, `.aws/`, `*.pem`, `*.key`, `id_rsa*`), `dropsite deploy` stops and lists them: pick the build output folder instead, or add `--skip-sensitive` to leave them out. Never add `--include-sensitive` unless the user explicitly confirmed those files are meant to be public.

## 3. Publish

```bash
dropsite deploy <folder> --json
```

- The first run creates a new site and writes `.dropsite.json` into the folder. Later runs update the same site; keep that file.
- To publish to an existing site, add `--site <id-or-slug>` (list them with `dropsite sites`).
- `--title "<name>"` sets the site name on first publish.

The JSON output has `url` (primary address), `urls` (all addresses) and `deploy_id`.

## 4. Report the result

Tell the user the `url` as a clickable link, and mention that:

- the page is live now; repeating the publish updates it in place;
- `dropsite rollback <site>` restores the previous version if something broke;
- a memorable address (slug such as `promo-may`), a password, own domains and form delivery are set in the web interface, or from a chat (ChatGPT, claude.ai) connected to dropsite.
