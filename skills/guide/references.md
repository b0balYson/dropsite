# Sites in the style of a reference

Users often show what they want instead of describing it: "make it like this site", a link, a screenshot, a photo of a menu board or a mood picture. Build an original site in a similar spirit, never a copy.

## A picture the user attached

You need no tool: look at the picture yourself. Note:

- palette: background, text, one or two accents, how much contrast;
- typography: serif or sans, heavy or light headings, condensed or wide, size contrast between headings and text;
- density: airy with large gaps or compact and information-rich;
- composition: full-bleed photo hero, split hero, editorial columns, cards, a single long story, a gallery grid;
- details and mood: rounded or sharp corners, shadows or flat, lines and borders, playful or strict.

## A link

Call inspect_reference with the address. It returns a design brief: palette with roles (background, text, accent), CSS variables, fonts and the size scale, spacing, corners, shadows, light or dark, the order of sections with their heading levels, and notable components (sticky header, hero type, cards, gallery, carousel, pricing, FAQ, form). If the server has screenshots, a picture of the first screen is attached; it is drawn without the reference's photos and web fonts. The brief never contains the page's texts or images, on purpose.

If the answer has little styling (`summary` says so), the site draws itself with scripts: ask the user for a screenshot. If the address cannot be fetched (`fetch_failed`), tell the user in one line and ask for a screenshot. Pages behind a login cannot be inspected.

## What to take and what never to copy

Take the direction, then adapt it to the user's business, audience and content: the palette (shift hues slightly or keep only the accent idea), the type pairing (use the same or a similar free font shipped as WOFF2, or a system font of the same kind), density, the order of sections and the mood.

Never copy one-to-one:

- texts, slogans, prices, reviews or names from the reference: write the user's own;
- photos, illustrations, icons and videos: use the user's own or freely licensed ones;
- logos, brand names, trademarks and anything that makes the site look like the other company;
- a distinctive layout reproduced pixel by pixel, or the reference's code.

Tell the user once, plainly: "I made it in the spirit of that site: similar colours, fonts and structure, with your own texts and photos. It is not a copy." If the user asks for an exact copy of someone else's site, explain that copying another company's design and content is not allowed and offer a site in a similar style instead.

## Why there is no robots.txt check

inspect_reference reads one page and its same-site stylesheets once, because the user asked, like opening the page in a browser; it does not crawl. It identifies itself with a generic `dropsite-reference` User-Agent and sends no personal data. Each user can inspect 30 pages an hour.
