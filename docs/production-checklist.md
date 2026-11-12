# Production acceptance checklist

The automated end-to-end test (`make e2e`) runs the whole stack with a local certificate authority. Real certificates and DNS can only be checked on the target server. Run through this list after the first installation and after major upgrades.

- [ ] `docker compose ps`: `caddy` is running, `control` is `healthy`, `caddy-init` exited with code 0.
- [ ] The web interface opens on `UI_URL` from the private network and **not** from the internet (`curl --max-time 5 http://<public IP>:8082` fails).
- [ ] The first administrator signs in.
- [ ] A new user receives an invite, sets a password and signs in.
- [ ] A new site uploaded as zip opens on `https://<random>.<domain>/` with a valid certificate (`curl -sI https://<random>.<domain>/` without `-k`).
- [ ] After setting a slug the site opens on both addresses; a taken or reserved slug is rejected.
- [ ] `dropsite deploy ./dist` prints a URL; running it again updates the same site.
- [ ] Rollback returns the previous content.
- [ ] Binding the root shows the site on `https://<domain>/` with a valid certificate.
- [ ] `https://api.<domain>/api/v1/openapi.json` opens with a valid certificate.
- [ ] `curl -sk https://<server IP>/` fails the TLS handshake (no certificate for IP access).
- [ ] `docker compose stop control`: published sites still open; `docker compose restart caddy` with `control` still stopped: sites still open; then `docker compose start control`.
- [ ] A backup is taken and copied off the server.
- [ ] Claude Code with the skill publishes a folder on "publish this site".

After switching to wildcard mode ([tls.md](tls.md#wildcard-mode)):

- [ ] `docker compose logs caddy` shows `certificate obtained successfully` for `*.<domain>` and `<domain>`.
- [ ] A newly created site opens with a valid certificate whose subjectAltName is `*.<domain>`, and no new per-address certificate appears in the log.
- [ ] The `preview` link of a release in the history opens that release and answers with `X-Robots-Tag: noindex` (`curl -sI`).
