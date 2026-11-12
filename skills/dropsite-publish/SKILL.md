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
- Allowed external links: ordinary `<a href>` links (maps, social networks, messengers), analytics counters that dropsite inserts itself when the user turns them on (do not add counter code by hand), and maps, videos or chat widgets the user explicitly asked for (see "Maps, videos and chat widgets").
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

## Accounts, databases and logins (external backend)

dropsite serves static files only. When the user needs sign-in, a database, a shared list or file uploads, connect the site to a backend service the user owns: Supabase, Firebase, PocketBase or Appwrite. Use it only when a form submission is not enough; a simple request form needs no backend (see "Forms on the site").

- Ask the user to create the project in that service themselves and to send you only the **public** values: the project URL and the public key (Supabase `anon` or `sb_publishable_…` key, Firebase web config with its `apiKey`, Appwrite project ID, PocketBase URL). These are designed to be shipped in a web page.
- Never put a secret key into the site: no Supabase `service_role` or `sb_secret_…` key, no Firebase service account JSON or Admin SDK key, no Appwrite API key, no PocketBase admin password. Everything in the site is public. dropsite refuses to publish files that contain such keys. If the user pastes one into the chat, do not use it and tell them to rotate it in the service.
- The public key is safe only with access rules. Before going live, tell the user to turn on and check them: Supabase Row Level Security on every table with policies for exactly what visitors may read or write; Firebase Security Rules (never `allow read, write: if true`); Appwrite collection permissions; PocketBase API rules. Write the rules for them as text they paste into the service.
- Ship the SDK with the site: download the ES module build into the site (`fetch` entry or save it into the folder, for example `js/supabase.js`) and import it by a relative path. No CDN links (jsDelivr, unpkg, esm.sh, gstatic).
- The service's own sign-in redirect and allowed origins must include the site's address: tell the user to add the link (and their own domain, if any) in the service settings.
- Keep the page working when the backend is down: show a short message instead of a blank page.

## Payments

A site published with dropsite never handles card data: no fields for card numbers, CVC or expiry dates, and no payment code of your own.

- Use the payment link of the user's payment service: YooKassa (payment links or a payment form link), CloudPayments (payment link or widget page from their account), Stripe Payment Links, or similar. Ask the user to create the link (one per product or price) in their account and send it to you; never invent one.
- Put the link on a clear button ("Pay 1 500 ₽", "Buy now") as an ordinary `<a href>`; the payment page opens on the service's site. Set the return address in the service to a page of the site (for example `thanks.html`).
- When the price depends on the order (size, quantity, delivery), add an order form that posts to `/_form/order` with the customer's contacts and choices, and tell the user that they send the payment link after confirming the order. Offer to connect Telegram or e-mail so orders arrive at once.
- Show prices, what is included, delivery and refund terms, and the seller's details (name, tax number, contacts) next to the button: payment services and the law require them.
- Never ask for secret keys of a payment service and never put them into the site (Stripe `sk_live_…` keys are refused at publication).

## Booking and appointments

dropsite does not keep a calendar and does not count free seats or time slots. Say this plainly when the user asks for booking, and offer one of two ways:

- **The user's booking service** (YCLIENTS, Calendly, Cal.com, Google Calendar appointment pages, a restaurant booking system): ask for their booking link and put it on a clear "Book" button as an ordinary link. Embed their widget only if the user asks for it (see "Maps, videos and chat widgets").
- **A booking request form**: a form posting to `/_form/booking` with name, phone, preferred date and time (`<input type="date">`, `<input type="time">` or a short list of slots) and a comment. Say on the page that this is a request and that the business confirms it by phone or message; the thanks page repeats it. Offer to connect Telegram so requests arrive at once.

Never promise that a slot is reserved or that two people cannot pick the same time when the site only sends a request.

## Personal data, privacy policy and cookies

A form that collects names, phones or e-mails collects personal data. In Russia this falls under 152-FZ, in the EU under the GDPR. Do the basics on every site with a form or analytics; you are not a lawyer, so say that the texts are a template the user should check.

- **Consent checkbox** in every form with personal data: a required, unchecked `<input type="checkbox" name="consent" required>` with a label like "I agree to the processing of my personal data under the privacy policy", where "privacy policy" links to the policy page. Never pre-check it. Russian sites: a separate consent is needed for marketing mailings.
- **Privacy policy page** (`privacy.html`), linked from every form and the footer: who processes the data (the business name, tax number, contacts), what data is collected and why, where it goes (dropsite stores requests and forwards them to the owner's channels), how long it is kept, how to ask for deletion. Ask the user for the business details; never invent them.
- **Cookie banner** only when analytics counters are on (Yandex Metrica, Google Analytics set through set_counters): a small banner at the bottom saying the site uses cookies for statistics, with an "OK" button that hides it and remembers the choice in `localStorage`. For EU visitors, counters should start only after consent; tell the user this if their audience is in the EU. dropsite's own visit statistics use no cookies and need no banner.
- Keep forms minimal: ask only for the data the business really needs.

## Search engines and link previews (SEO)

Do these on every public site; they cost little and help people find it.

- Every page: a unique `<title>` (under 60 characters, the business name and what it offers), `<meta name="description">` (under 160 characters), one `<h1>`, `<link rel="canonical">` with the page's full address, and text that names the city and the service in plain words.
- **Structured data**: one `<script type="application/ld+json">` per page with schema.org data that matches what the page shows. `LocalBusiness` (or a more exact type such as `Restaurant`, `BeautySalon`) with name, address, phone, opening hours and the site URL for a business; `Product` with `offers` (price, currency) for goods; `Event` with date, place and tickets for events; `FAQPage` for a questions-and-answers block. Never mark up anything that is not on the page.
- **robots.txt** at the root: `User-agent: *`, `Allow: /` and `Sitemap: <site address>/sitemap.xml`. If the site is hidden from search engines (set_site_access), dropsite already asks them not to index it: do not fight it with robots.txt.
- **sitemap.xml** at the root listing every page by its full address (use the address the user will share: their own domain if one is attached, otherwise the primary link). Update it when pages are added.
- **Open Graph** for link previews in messengers: `og:title`, `og:description`, `og:url`, `og:type`, `og:image` with a 1200×630 image file of the site (full address), and `twitter:card` = `summary_large_image`.
- After publishing, the user can add the site to Yandex Webmaster and Google Search Console; verification by an HTML file or a meta tag works on dropsite (add the file or tag to the site and publish).

## Sites in several languages

- Keep the main language at the root (`index.html`, `about.html`) and every other language in its own folder with the same file names: `/en/index.html`, `/en/about.html`. Do not switch languages with JavaScript on one page: search engines and link previews see only one language then.
- Every page sets its language (`<html lang="ru">`, `<html lang="en">`) and lists all its versions in `<head>`: `<link rel="alternate" hreflang="ru" href="<full address>/about.html">`, `<link rel="alternate" hreflang="en" href="<full address>/en/about.html">` and `hreflang="x-default"` pointing to the main version.
- A language switcher in the header: plain links to the same page in the other language (`RU` / `EN`), the current one marked with `aria-current="page"`. Never redirect visitors automatically by browser language.
- Translate everything the visitor sees: titles, descriptions, `alt` texts, buttons, form labels, the thanks page and `404.html`. Forms in every language may post to the same `/_form/<name>`; add a hidden field `lang` so the owner knows which version the request came from.
- List every language version in `sitemap.xml` (see "Search engines and link previews").
- Ask the user which languages they need and who checks the translation; offer your translation as a draft.

## Quizzes and calculators

Lead quizzes ("pick a kitchen in 5 questions") and calculators (price, delivery, loan) work on dropsite as plain JavaScript in the page; the result arrives as an ordinary form submission.

- Build it as one `<form action="/_form/quiz" method="post">` with steps (`<fieldset>` per question, shown one at a time), real `<input type="radio">`, `<input type="checkbox">` and `<input type="range">` with labels, "Back" and "Next" buttons and a progress indicator. Without JavaScript all questions show at once and the form still sends.
- Keep the script small and in a site file (`js/quiz.js`, loaded with `defer`); no frameworks or quiz builders from other sites.
- The last step asks for contacts (name, phone or messenger), the consent checkbox (see "Personal data, privacy policy and cookies"), the honeypot and the Altcha widget as in every form. Send every answer as its own named field (`area`, `style`, `budget`), and put a calculated result in a hidden field (`<input type="hidden" name="estimate">`), so the owner sees the answers and the estimate in the request.
- Show the result on the page (for example "About 120 000–150 000 ₽") and say clearly that it is an estimate and the final price comes after contact.
- Prices and formulas come from the user; never invent them. Keep them in one place at the top of the script so the user can ask you to change them later.
- Offer to connect Telegram so quiz requests arrive at once.

## Maps, videos and chat widgets

Embedded maps, videos and chat widgets load code from other companies, slow the page down and can track visitors. Add them only when the user explicitly asks; otherwise use plain links.

- **Default**: an address is a link to Yandex Maps or Google Maps; a video is a preview image of the site linking to the video page; a messenger is a `https://t.me/<name>` or `https://wa.me/<number>` link.
- **When the user asks for an embed**, use a lightweight facade: show a local preview image (a map screenshot or the video thumbnail saved into the site) with a "Show map" or play button, and insert the `<iframe>` only when the visitor clicks it. Give the iframe `title`, `loading="lazy"`, a fixed aspect ratio and `allowfullscreen` where it fits. For YouTube use the `https://www.youtube-nocookie.com/embed/<id>` address.
- **Chat widgets** (JivoSite, Telegram widgets, others) need the user's own widget code from their account; never invent one. Load the script only after the visitor clicks a "Chat" button or after the page has loaded, never in `<head>`.
- Embeds set cookies: mention them in the privacy policy, and with analytics on, in the cookie banner (see "Personal data, privacy policy and cookies").
- An embed is the one place where an external script or frame is allowed, because the user asked for it; everything else still ships with the site (see "Self-contained, fast and accessible sites").

After publishing a site with a form, tell the user to open the site in the dropsite web interface and connect a channel in the **Submissions** block (Telegram, e-mail or a webhook; optionally turn on **Require Altcha**); until then submissions are stored but not forwarded.

## Errors

The CLI prints the server message. Common ones:

| Message | What to do |
| --- | --- |
| no `index.html` | Publish the folder that contains `index.html`, or rename the main page |
| archive too large, too many files, file too large | Remove unused assets (videos, source images) or compress them, then retry |
| symbolic link | Replace symlinks with real files |
| secret keys found (`secrets_found`, a list of file:line) | Remove the secret keys from those files (use only public keys on a site) and tell the user to rotate them; `--include-sensitive` does not override this |
| site limit reached | Ask the user to delete an old site or contact the administrator |
| too many requests | Wait the time the CLI shows, then retry once |
