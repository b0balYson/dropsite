# External domains

A site can open on its owner's own domain (for example `promo.example.com`) in addition to its addresses under `BASE_DOMAIN`. An administrator approves every such domain: until then it serves nothing and gets no certificate, so nobody can make the server request certificates for arbitrary names or take over a domain they do not control.

## For site owners

1. **Add the domain** on the site page (block **Own domains**), with the CLI or the API:

   ```sh
   dropsite domains add promo-may promo.example.com
   dropsite domains promo-may            # every address with its status
   dropsite domains remove promo-may promo.example.com
   ```

   API: `POST /api/v1/sites/{id}/hostnames` with `{"hostname": "promo.example.com"}`, `GET /api/v1/sites/{id}/hostnames`, `DELETE /api/v1/sites/{id}/hostnames/{hostname}` (see [api-contract.md](api-contract.md)).

2. **Point the domain at the server** at your DNS provider:

   | Domain | Record |
   | --- | --- |
   | Subdomain, e.g. `promo.example.com` | `CNAME` to the site's random address, e.g. `k3f9x2m7qa.<BASE_DOMAIN>` |
   | Root of a domain, e.g. `example.com` | `A` (and `AAAA`) with the server's IP addresses |

   The site page shows the exact target and the server addresses.

3. **Wait for approval.** The status is shown on the site page, in `dropsite domains` and in the API:

   | Status | Meaning |
   | --- | --- |
   | `pending` (awaiting approval) | The administrator has not decided yet; the domain serves nothing. |
   | `active` (approved) | The domain serves the site; the HTTPS certificate is issued on the first visit. |
   | `rejected` | The administrator refused the domain or withdrew the approval; the reason is shown. Remove the domain and add it again to ask once more. |

Rules for the name:

- It is stored in lower case and punycode, without a trailing dot: `Промо.Пример.РФ` becomes `xn--l1adbbf.xn--e1afmkfd.xn--p1ai`.
- At least two labels; no scheme, path, port or IP address.
- Not the base domain or anything below it (use a slug for that), not the API host or below it, and not the web interface's host or one of its parent domains (a site there could plant cookies for the interface).
- Unique in the whole installation, in any status. A domain that another site already holds is refused without naming that site; ask the administrator if it is yours.
- At most `MAX_DOMAINS_PER_SITE` (default 5) per site, in any status.

Deleting a site releases its domains at once. A transferred site keeps its domains.

## For administrators

The **Domains** screen (`/admin/domains`) lists external domains with the owner's email and site, filtered by status (pending by default) and searchable. For each domain it loads a DNS check:

| Result | Meaning |
| --- | --- |
| Points at this server | Every A/AAAA address (after following a CNAME) is one of the server's addresses. |
| Points elsewhere | At least one address is not the server's. |
| No A/AAAA records | The name does not resolve. |
| Check failed | DNS error or no answer within 5 seconds. |
| Server addresses unknown | Neither `SERVER_IPS` nor the base domain resolves; set `SERVER_IPS`. |

The check is informational: approval is possible either way, for example before the owner switches DNS. The server's addresses are `SERVER_IPS` or, when it is empty, the A/AAAA records of `BASE_DOMAIN` and the API host (cached for 5 minutes). Only domains already added to a site are checked, and only by administrators, so the check cannot be used to probe arbitrary names.

- **Approve** makes the domain serve its site immediately; Caddy issues the certificate on the first visit.
- **Reject** (pending) or **Withdraw approval** (approved) requires a reason of 1–200 characters, which the owner sees; the domain stops serving at once.
- To free a domain held by the wrong site, open that site (administrators see every site) and remove the domain there.

Every addition, removal, approval and rejection is recorded in the audit log; DNS checks are not.

API: `GET /api/v1/admin/domains?status=pending|active|rejected|all`, `POST /api/v1/admin/domains/{hostname}/approve`, `POST .../reject` with `{"reason": "..."}`, `POST .../dns-check`.

## How it works

Caddy serves a host only through an entry in `/data/by-host/<hostname>`, and its certificate permission check (`ask`, answered by Caddy itself) allows a name only if that entry exists. `control` creates the entry when the domain is approved and removes it when the approval is withdrawn, the domain is removed or the site is deleted; at start-up it reconciles `by-host` with the database, where only approved domains count. An unapproved domain therefore never gets a certificate: the TLS handshake fails.

This works the same in both TLS modes. In `wildcard` mode the wildcard certificate covers only `*.<BASE_DOMAIN>`; external domains are matched by the on-demand catch-all of the Caddyfile and get their own certificates. They are separate registered domains, so they do not count against the Let's Encrypt limit of `BASE_DOMAIN`.

After an approval is withdrawn, a certificate already loaded in Caddy's memory may still complete a handshake, but the request finds no `by-host` entry and gets 404; after a Caddy restart the handshake fails.

Browsers always send the host name in lower case. A request whose `Host` header has upper-case letters gets 404, and one that does not match the TLS name gets 421.

## Settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `MAX_DOMAINS_PER_SITE` | `5` | External domains per site, 1–100 |
| `SERVER_IPS` | empty | Public IPs of the server, comma separated; shown to owners as the A/AAAA target and used by the DNS check |
