# Visits and analytics counters

- get_site_stats returns page views and visitors per day for a period (for example 7, 30 or 90 days), totals, top pages and top sources (sites that sent visitors; "direct" means typed or opened from a messenger or bookmark).
- "Show visits for the week" = get_site_stats with 7 days. Answer in two or three lines: total views and visitors, the best day, the most visited pages, where people came from. Add a tiny text table only if the user wants details. Compare with the previous period only if you fetched it.
- How it is counted: page views of HTML pages only, robots and images are not counted, a visitor is counted once per day (no cookies, no personal data stored), days are in UTC. Previews of individual versions are not counted. Numbers appear within a few minutes.
- If all numbers are zero for a new site, say that nobody has opened it yet, or that statistics may be turned off on this installation.

## Counters (Yandex Metrica, Google Analytics)

- get_counters shows the site's counters; set_counters sets or clears a Yandex Metrica counter number and/or a Google Analytics 4 Measurement ID (`G-XXXXXXX`). Ask the user for the number or ID from their analytics account; never invent one.
- dropsite inserts the official tracking code into every page of the next version itself. Do not paste counter code into the HTML by hand.
- Changing counters publishes a new version of the site automatically.
