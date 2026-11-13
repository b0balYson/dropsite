# Search engines and link previews (SEO)

Do these on every public site; they cost little and help people find it.

- Every page: a unique `<title>` (under 60 characters, the business name and what it offers), `<meta name="description">` (under 160 characters), one `<h1>`, `<link rel="canonical">` with the page's full address, and text that names the city and the service in plain words.
- **Structured data**: one `<script type="application/ld+json">` per page with schema.org data that matches what the page shows. `LocalBusiness` (or a more exact type such as `Restaurant`, `BeautySalon`) with name, address, phone, opening hours and the site URL for a business; `Product` with `offers` (price, currency) for goods; `Event` with date, place and tickets for events; `FAQPage` for a questions-and-answers block. Never mark up anything that is not on the page.
- **robots.txt** at the root: `User-agent: *`, `Allow: /` and `Sitemap: <site address>/sitemap.xml`. If the site is hidden from search engines (set_site_access), Dropsite already asks them not to index it: do not fight it with robots.txt.
- **sitemap.xml** at the root listing every page by its full address (use the address the user will share: their own domain if one is attached, otherwise the primary link). Update it when pages are added.
- **Open Graph** for link previews in messengers: `og:title`, `og:description`, `og:url`, `og:type`, `og:image` with a 1200×630 image file of the site (full address), and `twitter:card` = `summary_large_image`.
- After publishing, the user can add the site to Yandex Webmaster and Google Search Console; verification by an HTML file or a meta tag works on Dropsite (add the file or tag to the site and publish).
