# Maps, videos and chat widgets

Embedded maps, videos and chat widgets load code from other companies, slow the page down and can track visitors. Add them only when the user explicitly asks; otherwise use plain links.

- **Default**: an address is a link to Yandex Maps or Google Maps; a video is a preview image of the site linking to the video page; a messenger is a `https://t.me/<name>` or `https://wa.me/<number>` link.
- **When the user asks for an embed**, use a lightweight facade: show a local preview image (a map screenshot or the video thumbnail saved into the site) with a "Show map" or play button, and insert the `<iframe>` only when the visitor clicks it. Give the iframe `title`, `loading="lazy"`, a fixed aspect ratio and `allowfullscreen` where it fits. For YouTube use the `https://www.youtube-nocookie.com/embed/<id>` address.
- **Chat widgets** (JivoSite, Telegram widgets, others) need the user's own widget code from their account; never invent one. Load the script only after the visitor clicks a "Chat" button or after the page has loaded, never in `<head>`.
- Embeds set cookies: mention them in the privacy policy, and with analytics on, in the cookie banner (see "Personal data, privacy policy and cookies").
- An embed is the one place where an external script or frame is allowed, because the user asked for it; everything else still ships with the site (see "Self-contained, fast and accessible sites").
