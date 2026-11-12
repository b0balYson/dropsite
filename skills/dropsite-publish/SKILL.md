---
name: dropsite-publish
description: Publish a folder of static files (HTML, CSS, JS, images) to the team's dropsite server and return a public HTTPS link, and build contact or lead forms that work on dropsite without third-party form services. Use when the user asks to publish, deploy, share, upload or "put online" a landing page, site, prototype or HTML file built in this project, asks for a link to it, or wants a form (lead, contact, order, callback) on a site that will be published with dropsite.
---

# Publish a static site with dropsite

dropsite hosts finished static files. It does not build projects: if the project needs a build step (`npm run build`, `vite build`, `astro build`, `next build && next export`), run it first and publish the output folder.

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

Hidden files and `.git`, `node_modules`, `.DS_Store` are skipped automatically, but check that nothing private is in the folder.

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
- a memorable address (slug such as `promo-may`) can be set in the web interface.

## Forms on published sites

dropsite receives form submissions itself, so never use Formspree, Google Forms, Netlify Forms or other services for a site published here, and never write a backend. Every form posts to `/_form/<name>` on the same site; the site owner chooses where submissions go (Telegram, e-mail, webhook) on the site screen of the web interface.

Generate forms like this, always with the honeypot and the Altcha widget (the widget is harmless until the owner turns the check on):

```html
<form action="/_form/lead" method="post">
  <label>Name <input name="name" required></label>
  <label>Phone <input name="phone" type="tel" required></label>
  <input name="_hp" tabindex="-1" autocomplete="off" aria-hidden="true"
         style="position:absolute;left:-10000px">
  <input type="hidden" name="_next" value="/thanks.html">
  <altcha-widget challenge="/_form/challenge"></altcha-widget>
  <button type="submit">Send</button>
</form>
<script async defer src="/_dropsite/altcha.js" type="module"></script>
```

- `<name>` in `/_form/<name>` is `a-z`, `0-9`, `-`, `_` (for example `lead`, `callback`, `order`); use different names for different forms.
- `_hp` is a honeypot: keep it empty and visually hidden (not `type="hidden"`). Fields starting with `_` are never delivered.
- `_next` is a path on the same site shown after sending; create that page (e.g. `thanks.html`). Without `_next` a neutral built-in "Thank you" page is shown.
- No file inputs: files are dropped and bodies over 64 KB are rejected.
- With JavaScript, post with `fetch("/_form/lead", {method: "POST", headers: {Accept: "application/json"}, body: new FormData(form)})`; the answer is `{"ok": true}` or `{"ok": false, "error": {"code", "message"}}`.
- Load `/_dropsite/altcha.js` from the site itself, never from a CDN. Do not add other captchas.

After publishing a site with a form, tell the user to open the site in the dropsite web interface and connect a channel in the **Submissions** block (and optionally turn on **Require Altcha**); until then submissions are stored but not forwarded.

## Errors

The CLI prints the server message. Common ones:

| Message | What to do |
| --- | --- |
| no `index.html` | Publish the folder that contains `index.html`, or rename the main page |
| archive too large, too many files, file too large | Remove unused assets (videos, source images) or compress them, then retry |
| symbolic link | Replace symlinks with real files |
| site limit reached | Ask the user to delete an old site or contact the administrator |
| too many requests | Wait the time the CLI shows, then retry once |
