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

## Self-contained, fast and accessible sites

Sites published with dropsite must work on their own: no part of the page may depend on another server to load or look right.

### Ship everything with the site

- Every image, font, icon, script and stylesheet is a file of the site. No Google Fonts, no CDN links (jsDelivr, unpkg, cdnjs), no hotlinked photos (Unsplash, Pexels, other sites), no external icon kits.
- To use a picture or a font from the web, download it into the site: in a chat, add a `fetch` entry (`url` + `path` inside the site) to update_site_files or upload_files and the server downloads it; when publishing a folder, save the file into the folder. Then reference it by a relative path (`img/hero.webp`, `fonts/inter.woff2`).
- Use only images and fonts the user owns or that are free to use (for example, fonts under the SIL Open Font License, photos with a free license). Never take images from someone else's site.
- Allowed external links: ordinary `<a href>` links (maps, social networks, messengers), and analytics counters that dropsite inserts itself when the user turns them on (do not add counter code by hand).
- Prefer system fonts (`font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`). If a brand font matters, ship one or two WOFF2 files with `font-display: swap`.
- Keep JavaScript minimal; a typical business site needs none, except the form widget described under "Forms on the site".

### Fast

- Images: WebP (or AVIF) for photos, SVG for logos and icons, PNG only for screenshots with text. Size them for their display width: about 1600 px wide for a full-width hero, 800 px for cards, 400 px for thumbnails; quality around 75–80. A photo should usually be 50–250 KB, a whole page under 1–2 MB.
- Always set `width` and `height` on `<img>` and use `loading="lazy"` for images below the first screen; the first big image gets `fetchpriority="high"`. Use `srcset` when you ship two sizes.
- One stylesheet, small and without frameworks pulled from the web. No render-blocking scripts in `<head>` (use `defer`).
- No autoplaying video; a video belongs on a video platform behind a plain link or a lightweight preview image.

### Mobile first and accessible

- `<meta name="viewport" content="width=device-width, initial-scale=1">`, layouts that work at 360 px wide first, then grow; no horizontal scrolling; tap targets at least 44 px.
- `<html lang="…">` in the page language, one `<h1>`, headings in order, real `<button>` and `<a>` elements, `<label>` for every form field, meaningful `alt` text for images (empty `alt=""` for decorative ones).
- Text contrast at least 4.5:1; do not convey meaning by color alone; visible focus outlines; respect `prefers-reduced-motion`.
- Phone numbers as `tel:` links, e-mail as `mailto:` links, addresses as links to a map.
- Add `<title>`, `<meta name="description">`, Open Graph tags (`og:title`, `og:description`, `og:image` with a site file), a favicon and `404.html`.

## Forms on the site

dropsite receives form submissions itself: never use Formspree, Google Forms, Netlify Forms or other form services for a site published here, and never write a backend. Every form posts to `/_form/<name>` on the same site.

Generate forms like this, always with the honeypot and the Altcha widget (the widget is harmless until the owner turns the check on):

```html
<form action="/_form/lead" method="post">
  <label>Name <input name="name" autocomplete="name" required></label>
  <label>Phone <input name="phone" type="tel" autocomplete="tel" required></label>
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
- `_next` is a path on the same site shown after sending; create that page (for example `thanks.html`). Without `_next` a neutral built-in "Thank you" page is shown.
- No file inputs: files are dropped and bodies over 64 KB are rejected. At most 50 fields.
- With JavaScript, post with `fetch("/_form/lead", {method: "POST", headers: {Accept: "application/json"}, body: new FormData(form)})`; the answer is `{"ok": true}` or `{"ok": false, "error": {"code", "message"}}`.
- Load `/_dropsite/altcha.js` from the site itself, never from a CDN. Do not add other captchas.
- Forms work only on the site's own address (posts from other sites are refused). On a password-protected site only visitors who signed in can send the form.

After publishing a site with a form, tell the user to open the site in the dropsite web interface and connect a channel in the **Submissions** block (Telegram, e-mail or a webhook; optionally turn on **Require Altcha**); until then submissions are stored but not forwarded.

## Errors

The CLI prints the server message. Common ones:

| Message | What to do |
| --- | --- |
| no `index.html` | Publish the folder that contains `index.html`, or rename the main page |
| archive too large, too many files, file too large | Remove unused assets (videos, source images) or compress them, then retry |
| symbolic link | Replace symlinks with real files |
| site limit reached | Ask the user to delete an old site or contact the administrator |
| too many requests | Wait the time the CLI shows, then retry once |
