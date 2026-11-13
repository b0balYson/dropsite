# Self-contained, fast and accessible sites

Sites published with Dropsite must work on their own: no part of the page may depend on another server to load or look right.

## Ship everything with the site

- Every image, font, icon, script and stylesheet is a file of the site. No Google Fonts, no CDN links (jsDelivr, unpkg, cdnjs), no hotlinked photos (Unsplash, Pexels, other sites), no external icon kits.
- To use a picture or a font from the web, download it into the site: in a chat, add a `fetch` entry (`url` + `path` inside the site) to update_site_files or upload_files and the server downloads it; when publishing a folder, save the file into the folder. Then reference it by a relative path (`img/hero.webp`, `fonts/inter.woff2`).
- Use only images and fonts the user owns or that are free to use (for example, fonts under the SIL Open Font License, photos with a free license). Never take images from someone else's site.
- Allowed external links: ordinary `<a href>` links (maps, social networks, messengers), analytics counters that Dropsite inserts itself when the user turns them on (do not add counter code by hand), and maps, videos or chat widgets the user explicitly asked for (see "Maps, videos and chat widgets").
- Prefer system fonts (`font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`). If a brand font matters, ship one or two WOFF2 files with `font-display: swap`.
- Keep JavaScript minimal; a typical business site needs none, except the form widget described under "Forms on the site".

## Fast

- Images: WebP (or AVIF) for photos, SVG for logos and icons, PNG only for screenshots with text. Size them for their display width: about 1600 px wide for a full-width hero, 800 px for cards, 400 px for thumbnails; quality around 75–80. A photo should usually be 50–250 KB, a whole page under 1–2 MB.
- Always set `width` and `height` on `<img>` and use `loading="lazy"` for images below the first screen; the first big image gets `fetchpriority="high"`. Use `srcset` when you ship two sizes.
- One stylesheet, small and without frameworks pulled from the web. No render-blocking scripts in `<head>` (use `defer`).
- No autoplaying video; a video belongs on a video platform behind a plain link or a lightweight preview image.

## Mobile first and accessible

- `<meta name="viewport" content="width=device-width, initial-scale=1">`, layouts that work at 360 px wide first, then grow; no horizontal scrolling; tap targets at least 44 px.
- `<html lang="…">` in the page language, one `<h1>`, headings in order, real `<button>` and `<a>` elements, `<label>` for every form field, meaningful `alt` text for images (empty `alt=""` for decorative ones).
- Text contrast at least 4.5:1; do not convey meaning by color alone; visible focus outlines; respect `prefers-reduced-motion`.
- Phone numbers as `tel:` links, e-mail as `mailto:` links, addresses as links to a map.
- Add `<title>`, `<meta name="description">`, Open Graph tags (`og:title`, `og:description`, `og:image` with a site file), a favicon and `404.html`.
