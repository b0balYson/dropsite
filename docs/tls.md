# TLS certificates

## On-demand mode (default)

Caddy obtains a separate certificate for each address the first time a visitor opens it. Before asking the certificate authority it checks a permission endpoint served by Caddy itself on `127.0.0.1:8090` inside the container: the answer is "yes" only when the address exists in the sites volume (`/data/by-host/<host>`). This check does not involve the `control` service, so sites keep working, and keep renewing, while `control` is down.

- New addresses (a new site, a new slug) get a certificate on the first visit, which takes a few seconds once.
- Removed addresses never get a new certificate.
- Requests without a known name (direct IP access, unknown subdomains) fail the TLS handshake.

### Let's Encrypt rate limits

As of 2026-08-05 (<https://letsencrypt.org/docs/rate-limits/>):

| Limit | Value | Refill |
| --- | --- | --- |
| New certificates per registered domain | 50 per 7 days | 1 every 202 minutes |
| New orders per account | 300 per 3 hours | 1 every 36 seconds |
| Certificates for the exact same set of names | 5 per 7 days | 1 every 34 hours |
| Failed validations per name per account | 5 per hour | 1 every 12 minutes |
| New accounts per IP | 10 per 3 hours | |

Renewals through ARI do not count against these limits.

What it means for pushpage: each address is one certificate. A site with a random address and a slug costs two, so a single installation can add roughly **25 new sites per week**. Changing a slug costs one more. The base domain root and the API host cost one each, once.

Signs that you hit the limit:

- a new address fails the TLS handshake for a long time while older sites work;
- Caddy logs contain `rateLimited` or `too many certificates` from `acme-v02.api.letsencrypt.org`.

Caddy automatically retries with ZeroSSL when Let's Encrypt refuses, which usually hides short spikes.

Certificate lifetimes are getting shorter (the `classic` profile goes to 64 days on 2027-02-10 and to 45 days on 2028-02-16). Caddy renews automatically; renewals do not consume the limits above.

### Privacy

Every certificate is published in Certificate Transparency logs. Anyone can list all addresses of the installation. Do not rely on an address being unguessable.

## Wildcard mode (not in this version)

A single certificate for `*.<BASE_DOMAIN>` removes the per-address limit and keeps addresses out of CT logs (only the wildcard is logged). It requires the DNS-01 challenge, which means:

1. a Caddy build with the DNS provider plugin for your DNS host (for example `xcaddy build --with github.com/caddy-dns/cloudflare`);
2. an API token for the DNS provider in the Caddy environment;
3. a `tls { dns <provider> ... }` block for `*.{$BASE_DOMAIN}` and the apex instead of `on_demand`.

`TLS_MODE=wildcard` is rejected by `control` in this version. Switch when you approach the Let's Encrypt limit; plan it as a separate change.
