# Installation details

The [README](../README.md) has the 5-minute path. This page covers the choices behind it.

## Server

- Linux, x86-64 or arm64, Docker Engine 24+ with the Compose plugin.
- Sizing target: 2 vCPU and 4 GB RAM serve up to 10 000 sites and 1000 users. The `control` service idles below 100 MB.
- Open ports 80 and 443 (TCP) and 443/UDP for HTTP/3. Port 8082 must stay closed to the internet.

## Domain and DNS

1. Pick a dedicated base domain (see the security note in the README).
2. Create `A`/`AAAA` records for the domain and for `*.<domain>` pointing at the server.
3. The API lives on `api.<domain>` by default (`API_HOST`). The slug `api` and other system names are reserved automatically.

Certificates are issued on first visit; see [tls.md](tls.md) for limits.

## Files

| File | Purpose |
| --- | --- |
| `docker-compose.yml` | Services `caddy-init`, `caddy`, `control`; volumes `sites`, `state`, `caddy_data`, `logs` |
| `Caddyfile` | Static Caddy configuration; do not edit unless you know why each rule is there |
| `.env` | Your settings, from `.env.example` |

Volumes:

| Volume | Mounted in | Contents |
| --- | --- | --- |
| `sites` | control (rw), caddy (ro) | releases and host-name symlinks |
| `state` | control | SQLite database, temporary uploads |
| `caddy_data` | caddy | certificates and ACME account | | `logs` | caddy (rw), control (ro) | access log of published sites for statistics, rotated by Caddy (see [statistics.md](statistics.md)) |

## Web interface access

The interface is served over plain HTTP on `ADMIN_BIND_IP:8082` and must only be reachable from a private network:

- **Single admin:** keep the default `127.0.0.1` and use `ssh -L 8082:127.0.0.1:8082 server`.
- **Team:** join the server to an overlay network (Tailscale, WireGuard, NetBird), set `ADMIN_BIND_IP` to its overlay address and `UI_URL=http://<overlay address>:8082`, then `docker compose up -d`.

Invite links use `UI_URL`; make sure it is the address your colleagues can open.

## First steps

1. Sign in with `ADMIN_EMAIL` and `ADMIN_PASSWORD`. The password variable is only read on the first start; change it later by issuing yourself a new invite (Users → reissue invite).
2. Create users and send them invite links.
3. Optionally bind a site to the base domain root (All sites → bind root).
4. Review limits in Settings.
