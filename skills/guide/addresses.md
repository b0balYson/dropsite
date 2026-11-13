# Addresses

Every site has a random address such as `k3f9x2m7qa.example.site`. Other addresses are optional.

## Memorable address (slug)

- update_site (or set_slug) sets an address label such as `coffee-river`, which opens the site at `coffee-river.<base domain>`. Lowercase latin letters, digits and hyphens. An empty value removes it. The random address keeps working.
- If the label is taken, suggest two or three variants. A new address may take up to a minute to get its HTTPS certificate on the first visit.

## Own domain

1. add_domain with the site and the domain (for example `coffee.example.com` or `example.com`). It returns the DNS records to create and the domain's status.
2. Tell the user, step by step and in plain words, what to do at the company where the domain was bought (their DNS provider). Show the exact record values returned by the tool:
   - for a subdomain (`coffee.example.com`): a record of type CNAME, name `coffee`, value = the site's random address;
   - for a root domain (`example.com`): records of type A (and AAAA, if given) with the server's IP addresses, name `@`.
   Remove any old A/AAAA/CNAME records for the same name. Changes usually apply within minutes, sometimes up to a day.
3. A Dropsite administrator must approve the domain before it serves the site. Until then the status is "awaiting approval". After approval the HTTPS certificate is issued on the first visit.
4. list_domains shows each domain's status: awaiting approval, active (works), rejected (with the reason; remove and add again to ask once more).

remove_domain stops the domain at once: confirm first. A domain can belong to only one site in the whole installation; if it is taken, say so without guessing whose it is.

These steps happen outside Dropsite and you cannot do them: creating DNS records and the administrator's approval. Say that honestly.

## Root domain of the installation (administrators only)

The base domain itself (for example `example.site`) can show one site, such as a placeholder or the company page. Only an administrator of the installation can change it, and only with set_root_site (site + confirm) or clear_root_site (confirm). "Put this placeholder on the root domain" = publish or find the placeholder site, tell the administrator which site will replace the current one, wait for "yes", then call set_root_site. If the user is not an administrator, the tool refuses: say that an administrator of their Dropsite does this.
