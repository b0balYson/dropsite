# Backup and restore

A backup is one `tar.gz` with a consistent snapshot of the database (`VACUUM INTO`) and the whole sites volume, including releases and the symlinks that route host names. Secrets (password and token hashes) are inside: store backups as carefully as the server.

Servers set up by the [one-command installer](install.md#one-command-installer) already have `dropsite-backup` (daily at 03:15 from `/etc/cron.d/dropsite-backup`, the last 7 archives in `/var/backups/dropsite`, the secrets key copied once to `/root/dropsite-secrets.key`), `dropsite-update` and `dropsite-rollback`. Copy the archives and the key off the server yourself.

## Back up

The service keeps running:

```bash
docker compose exec -T control /control backup - > backup-$(date +%F).tar.gz
```

The database is captured first, then the files. A publish that finishes in between leaves an extra release directory in the backup; restore removes it, because the database is the source of truth.

Schedule it with cron and copy the file off the server, for example:

```cron
15 3 * * * cd /opt/dropsite/deploy && docker compose exec -T control /control backup - > /var/backups/dropsite-$(date +\%F).tar.gz
```

## Restore

Stop `control` (Caddy keeps serving the current files meanwhile), restore, start again:

```bash
docker compose stop control
docker compose run --rm -T control restore - --force < backup-2026-11-12.tar.gz
docker compose start control
```

- Without `--force` the restore refuses to overwrite an installation that already has data.
- After unpacking, `control` applies migrations and reconciles the sites volume with the database.
- A backup made by a newer version cannot be restored by an older one.

## The form secrets key

The Telegram bot token, the SMTP password and webhook signing secrets are stored in the database encrypted with `/state/secrets.key` (32 random bytes created on the first start). The key is deliberately **not** part of the backup, so a leaked backup does not reveal those secrets. Keep a copy of it next to your other server secrets:

```bash
docker compose cp control:/state/secrets.key ./secrets.key   # store it safely
docker compose cp ./secrets.key control:/state/secrets.key   # after a restore on a new server, before starting control
```

The file must stay readable by the `control` user (UID 65532), for example `sudo chown 65532:65532 secrets.key && chmod 600 secrets.key` before `docker compose cp -a`.

Restored without the key, everything else works, but the settings screen reports that stored secrets cannot be read: enter the bot token and SMTP password again, and site owners rotate their webhook secrets (submissions to those webhooks fail until then).

## Upgrade

On a server set up by the installer, `dropsite-update` does all of this and `dropsite-rollback` undoes it ([releasing.md](releasing.md#on-a-server)).

By hand, run these commands from `dropsite/deploy`, with `X.Y.Z` the new release. With wildcard TLS, keep `COMPOSE_FILE=docker-compose.yml:compose.wildcard.yml` in `.env` so the same mode is used for every command.

```bash
docker compose exec -T control /control backup - > before-upgrade.tar.gz
git fetch --tags
git checkout vX.Y.Z
DROPSITE_VERSION=X.Y.Z docker compose pull
DROPSITE_VERSION=X.Y.Z docker compose up -d --wait
docker compose ps
```

With `DROPSITE_VERSION=latest` in `.env` (the default) the variable on the command line is only needed until `latest` points at the new release on ghcr.io; with a pinned version, change it in `.env` instead. A checkout that follows `main` is upgraded with `git pull --ff-only` and `docker compose up -d --build --wait` (a source build).

Migrations run on start. Downgrading is not supported, and a newer database refuses to start with an older `control`: to go back, check out the previous tag, start its images (`DROPSITE_VERSION=<previous> docker compose pull`) or rebuild it, and restore the backup taken before the upgrade. Keep the same checkout path and Compose project name so Docker reuses the existing volumes. Do not run `docker compose down -v`: it deletes the data volumes.
