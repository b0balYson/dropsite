## Publishing static sites with dropsite

<!-- Generated from skills/parts and skills/guide: edit those files and run `go test ./skills -update`. -->

When the user asks to publish, deploy, share or "put online" a page or site from this project, publish it with the `dropsite` CLI and give them the link in plain words.

1. Check that the CLI is ready: `dropsite version && dropsite sites --json >/dev/null && echo READY`.
   - If `dropsite` is not found, or the error mentions `not logged in`, `dropsite login` or `401`, stop and ask the user to press **Connect** on the **Connect AI** page of their dropsite web interface and run the shown command in their own terminal. Never ask for a token or a connect code in the chat.
2. Choose the folder: it must contain `index.html` at its root. Build the project first if it has a build step and publish the output (`dist/`, `build/`, `out/`). Never publish a folder with source code, `.env` files or secrets.
3. Publish: `dropsite deploy <folder> --json`. The first run creates a site and writes `.dropsite.json` into the folder; later runs update the same site. Use `--site <id-or-slug>` to target an existing site and `--title "<name>"` to name a new one.
4. Reply with the `url` from the JSON output as a link. Mention that publishing again updates the same address and that `dropsite rollback <site>` restores the previous version.

If the CLI reports a missing `index.html`, a size or file-count limit, a symbolic link or a rate limit, explain the message to the user and fix the folder before retrying once. If it refuses secret-looking files (`.env*`, keys, credentials), publish the build output folder instead or add `--skip-sensitive`; never add `--include-sensitive` without the user's explicit confirmation.

### Self-contained, fast and accessible sites

Sites published with dropsite must work on their own: no part of the page may depend on another server to load or look right.

#### Ship everything with the site

- Every image, font, icon, script and stylesheet is a file of the site. No Google Fonts, no CDN links (jsDelivr, unpkg, cdnjs), no hotlinked photos (Unsplash, Pexels, other sites), no external icon kits.
- To use a picture or a font from the web, download it into the site: in a chat, add a `fetch` entry (`url` + `path` inside the site) to update_site_files or upload_files and the server downloads it; when publishing a folder, save the file into the folder. Then reference it by a relative path (`img/hero.webp`, `fonts/inter.woff2`).
- Use only images and fonts the user owns or that are free to use (for example, fonts under the SIL Open Font License, photos with a free license). Never take images from someone else's site.
- Allowed external links: ordinary `<a href>` links (maps, social networks, messengers), and analytics counters that dropsite inserts itself when the user turns them on (do not add counter code by hand).
- Prefer system fonts (`font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`). If a brand font matters, ship one or two WOFF2 files with `font-display: swap`.
- Keep JavaScript minimal; a typical business site needs none, except the form widget described under "Forms on the site".

#### Fast

- Images: WebP (or AVIF) for photos, SVG for logos and icons, PNG only for screenshots with text. Size them for their display width: about 1600 px wide for a full-width hero, 800 px for cards, 400 px for thumbnails; quality around 75–80. A photo should usually be 50–250 KB, a whole page under 1–2 MB.
- Always set `width` and `height` on `<img>` and use `loading="lazy"` for images below the first screen; the first big image gets `fetchpriority="high"`. Use `srcset` when you ship two sizes.
- One stylesheet, small and without frameworks pulled from the web. No render-blocking scripts in `<head>` (use `defer`).
- No autoplaying video; a video belongs on a video platform behind a plain link or a lightweight preview image.

#### Mobile first and accessible

- `<meta name="viewport" content="width=device-width, initial-scale=1">`, layouts that work at 360 px wide first, then grow; no horizontal scrolling; tap targets at least 44 px.
- `<html lang="…">` in the page language, one `<h1>`, headings in order, real `<button>` and `<a>` elements, `<label>` for every form field, meaningful `alt` text for images (empty `alt=""` for decorative ones).
- Text contrast at least 4.5:1; do not convey meaning by color alone; visible focus outlines; respect `prefers-reduced-motion`.
- Phone numbers as `tel:` links, e-mail as `mailto:` links, addresses as links to a map.
- Add `<title>`, `<meta name="description">`, Open Graph tags (`og:title`, `og:description`, `og:image` with a site file), a favicon and `404.html`.

### Forms on the site

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

After publishing a site with a form, tell the user to connect Telegram, e-mail or a webhook in the **Submissions** block of the site screen in the web interface.
