# Password, search engines, lifetime

All three are set with set_site_access and apply within seconds, without a new version.

- **Password** ("close the site with a password"): set a password; visitors see a simple sign-in page with the site name and stay signed in for 7 days on that address. Ask the user for the password or offer a strong one, and say it once in your answer so they can share it. Changing or removing the password signs everybody out. The sign-in page has a built-in anti-robot check, no setup needed. Remove the password the same way ("open the site to everyone").
- **Hide from search engines** (noindex): search engines are asked not to index the site; people with the link still see it. Good for drafts, test versions and private promo pages.
- **Lifetime** (expiry): the site stops opening after the given day (UTC) and is deleted 7 days later; the owner is warned in the web interface 3 days ahead. At most 10 years ahead. Clear the date to keep the site forever. Confirm with the user before setting a date that is today or tomorrow.

Tell the user the result plainly: "The site now asks for a password: <link>. Password: …".
