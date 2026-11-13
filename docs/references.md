# Design references

A user can show the assistant a site they like ("make it in this style") or attach a picture. The assistant builds an original site in a similar style, never a copy. A picture needs no server: the assistant looks at it itself (guide topic `references`). A link goes through `POST /api/v1/references/inspect` or the MCP tool `inspect_reference` (both servers).

## What the server does

1. Checks the address like every download of the server ([ai-clients.md](ai-clients.md), images and fonts downloaded by the server): `https://` on port 443 only, no user name or password, public addresses only. The address actually connected to is checked on every connection, including each of at most 3 redirects; no proxy is used.
2. Downloads the page: `text/html` or `application/xhtml+xml`, at most 2 MB.
3. Downloads the stylesheets of the same origin (scheme and host of the final page address) that the page links, and their `@import`s: at most 8 files, 512 KB each, 2 MB in total. Stylesheets of other servers are not downloaded; from Google Fonts links only the family names are read. A stylesheet that fails is skipped.
4. Builds the brief. The whole call is bounded by 30 seconds.

Each user may inspect 30 pages an hour (`rate_limited` with `retry_after` beyond that).

## The brief

`palette` (up to 8 `#rrggbb` colors with a role: `background`, `surface`, `text`, `muted`, `accent`, `accent2`, `border`, `other`), `custom_properties` (CSS variables with colors, sizes or fonts, up to 40), `fonts` (family, `headings`/`body`/`other`, whether it is a web font), `font_sizes` (largest first), `spacing` (`compact`, `regular`, `airy`), `radius` (`sharp`, `subtle`, `rounded`, `pill`), `shadows` (`none`, `subtle`, `strong`), `color_scheme` and `has_dark_mode`, `outline` (up to 30 sections in order: kind from a fixed vocabulary, heading levels, number of images, number of similar items), `components` (a fixed vocabulary: `sticky_header`, `hero_image`, `hero_split`, `hero_text`, `hero_video`, `cards`, `gallery`, `carousel`, `features`, `pricing`, `testimonials`, `team`, `faq`, `cta`, `form`, `map`, `video`), `stylesheets` (read, inline, skipped) and a one-paragraph `summary`.

The brief never contains the page's texts (only `<title>`, at most 120 characters), heading texts, image addresses or data, logos, scripts, class names or IDs. The analysis is a heuristic over frequencies, not a CSS engine: pages drawn by scripts give an almost empty brief, and the summary then suggests asking the user for a screenshot.

## Screenshot

With the optional renderer ([screenshots.md](screenshots.md)) the answer also has a PNG of the first screen (1280×800): `screenshot.status = "included"` and `image_png` in base64 over the API, an image block in MCP. The renderer still has no internet access. `control` gives it, by a one-time token through the render listener, a sanitized copy of the page held in memory: scripts, `noscript`, frames, objects, `<base>`, `meta http-equiv`, event handlers, `javascript:` links, image and video sources are removed, the downloaded stylesheets are served as local files, and a Content-Security-Policy forbids scripts. Pictures and web fonts of the reference are therefore missing on the screenshot. Without a renderer, or if it fails, the brief comes without a picture and `screenshot.status = "unavailable"`; this is not an error.

## robots.txt

robots.txt is not consulted. It governs crawlers; an inspection is a one-off download of one page and its own stylesheets on the explicit request of the user, like opening the page in a browser, and nothing is followed further. Requests carry the generic User-Agent `dropsite-reference`, without the server's address or any data about the user.
