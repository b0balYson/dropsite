# TLS certificates

Two modes, chosen by `TLS_MODE`: **ondemand** (default) issues a certificate per address on first visit; **wildcard** uses one certificate for all addresses under the base domain and needs a DNS provider API token. Start with ondemand and switch when you approach the Let's Encrypt limit below.

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

What it means for dropsite in ondemand mode: each address is one certificate. A site with a random address and a slug costs two, so a single installation can add roughly **25 new sites per week**. Changing a slug costs one more. The base domain root and the API host cost one each, once. The stable draft preview of a site, `https://p-<site id>.<BASE_DOMAIN>/`, costs one more certificate, once per site and only when a draft preview is first opened: later drafts reuse the same address and certificate (without a draft it answers 404).

Signs that you hit the limit:

- a new address fails the TLS handshake for a long time while older sites work;
- Caddy logs contain `rateLimited` or `too many certificates` from `acme-v02.api.letsencrypt.org`.

Caddy automatically retries with ZeroSSL when Let's Encrypt refuses, which usually hides short spikes.

Certificate lifetimes are getting shorter (the `classic` profile goes to 64 days on 2027-02-10 and to 45 days on 2028-02-16). Caddy renews automatically; renewals do not consume the limits above.

### Privacy

Every certificate is published in Certificate Transparency logs. Anyone can list all addresses of the installation. Do not rely on an address being unguessable.

## Wildcard mode

One certificate for `*.<BASE_DOMAIN>` and one for `<BASE_DOMAIN>` cover every site address: new sites, slugs and previews no longer issue certificates, so the weekly limit above stops mattering, and individual addresses stay out of CT logs (only `*.<BASE_DOMAIN>` is logged). A wildcard certificate requires the DNS-01 challenge: Caddy proves control of the domain by creating a TXT record through your DNS provider's API.

What changes:

- Addresses under the base domain (random, slug, root, previews) use the wildcard certificates. External domains outside the base domain still get their own certificate on first visit, with the same permission check.
- Every release gets a **preview address** `https://p-<release id>-<site id>.<BASE_DOMAIN>/` that serves exactly that release, whichever release is current. Previews answer with `X-Robots-Tag: noindex`, appear in the release history of the web UI and as `preview_url` in the API, and disappear when the release is pruned or the site is deleted. They exist only in this mode: in ondemand mode each preview would cost a certificate.
- Slugs of the form `p-<8 digits>-<6 digits>-<6 characters>-<10 characters>` are reserved in both modes.

### Requirements

- DNS of `BASE_DOMAIN` is hosted by a supported provider. Built in: **Cloudflare**.
- The full repository checkout. Compose builds Caddy 2.11.9 with the DNS plugin locally from [`deploy/caddy/Dockerfile`](../deploy/caddy/Dockerfile); no dropsite registry is used.
- `compose.wildcard.yml` next to `docker-compose.yml`.

### Switching to wildcard

1. **Create a Cloudflare API token.** Cloudflare dashboard → My Profile → API Tokens → Create Token → template *Edit zone DNS*. Permissions: `Zone` → `DNS` → `Edit`. Zone resources: *Include* → *Specific zone* → the zone of `BASE_DOMAIN`. Nothing else is needed; do not use the Global API Key.
2. **Check DNS.** The `A`/`AAAA` records for `<BASE_DOMAIN>` and `*.<BASE_DOMAIN>` keep pointing at the server. If the zone is proxied by Cloudflare (orange cloud), switch these records to *DNS only*: Caddy must terminate TLS itself.
3. **Add the token to `.env`** and leave `TLS_MODE` empty there (the override sets it for both containers):

   ```sh
   CF_API_TOKEN=<token>
   ```

4. **Start with the override:**

   ```sh
   docker compose -f docker-compose.yml -f compose.wildcard.yml up -d --build --wait
   ```

   Run this from `dropsite/deploy`. The override replaces stock Caddy with the local build, including its DNS plugin.

5. **Check.** In `docker compose logs caddy` look for `certificate obtained successfully` with `"identifier":"*.<BASE_DOMAIN>"` and `"identifier":"<BASE_DOMAIN>"` (the first issuance takes up to a minute while the TXT record propagates). Then:

   ```sh
   curl -sI https://<random>.<BASE_DOMAIN>/      # valid certificate, no -k
   echo | openssl s_client -connect <server>:443 -servername <random>.<BASE_DOMAIN> 2>/dev/null | openssl x509 -noout -ext subjectAltName
   # DNS:*.<BASE_DOMAIN>
   ```

   On start, `control` creates preview addresses for all existing releases.

Keep using `-f docker-compose.yml -f compose.wildcard.yml` with every `docker compose` command (or set `COMPOSE_FILE=docker-compose.yml:compose.wildcard.yml` in `.env`).

### Troubleshooting

- `CF_API_TOKEN is required in wildcard mode`: the token is missing from `.env`; compose refuses to start.
- `API token '...' appears invalid`: Caddy does not start; the value is truncated or quoted.
- `module not registered: dns.providers.cloudflare`: Caddy runs from the stock image; the override was not applied.
- Sites under the base domain fail the TLS handshake and the log shows DNS or ACME errors: the token lacks `Zone.DNS:Edit` on the right zone, or the zone is not on Cloudflare. Fix the token or roll back (below).

### Rolling back to ondemand

```sh
docker compose -f docker-compose.yml up -d --build --wait
```

Both containers return to ondemand mode (make sure `.env` has no `TLS_MODE=wildcard`). Caddy issues per-address certificates again on the next visit of each address, which counts against the limits above; `control` removes all preview addresses on start. The wildcard certificates stay in the `caddy_data` volume and are reused if you switch back.

### Another DNS provider

Caddy has plugins for most providers (<https://github.com/caddy-dns>). To add one, for example Route 53:

1. In `deploy/caddy/Dockerfile` add `--with github.com/caddy-dns/route53@<version>` to the `xcaddy build` command (pin the version) and build your own image.
2. In `deploy/Caddyfile` add a snippet next to `(dns_cloudflare)` with the provider's options (see the plugin's README), reading secrets with `{env.*}`:

   ```caddyfile
   (dns_route53) {
   	dns route53 {
   		access_key_id {env.AWS_ACCESS_KEY_ID}
   		secret_access_key {env.AWS_SECRET_ACCESS_KEY}
   	}
   }
   ```

3. In a copy of `compose.wildcard.yml` keep the local `build` configuration and set `TLS_DNS_PROVIDER: route53` and the provider's variables instead of `CF_API_TOKEN`.

The mode and provider are chosen by environment only: `import tls_{$TLS_MODE}` and `import dns_{$TLS_DNS_PROVIDER}` in the Caddyfile pick the snippets, so no other line changes. An unknown mode or provider stops Caddy at start-up with `File to import not found`.

### Local testing without DNS

`TLS_DNS_PROVIDER=internal` signs the wildcard certificate with Caddy's local CA and never calls a DNS API. The end-to-end test uses it:

```sh
E2E_WILDCARD=1 go test -count=1 -timeout 20m -tags e2e ./test/e2e/...
```
