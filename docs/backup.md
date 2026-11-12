# Backup and restore

A backup is one `tar.gz` with a consistent snapshot of the database (`VACUUM INTO`) and the whole sites volume, including releases and the symlinks that route host names. Secrets (password and token hashes) are inside: store backups as carefully as the server.

## Back up

The service keeps running:

```bash
docker compose exec -T control /control backup - > backup-$(date +%F).tar.gz
```

The database is captured first, then the files. A publish that finishes in between leaves an extra release directory in the backup; restore removes it, because the database is the source of truth.

Schedule it with cron and copy the file off the server, for example:

```cron
15 3 * * * cd /opt/dropsite && docker compose exec -T control /control backup - > /var/backups/dropsite-$(date +\%F).tar.gz
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

```bash
docker compose exec -T control /control backup - > before-upgrade.tar.gz
docker compose pull && docker compose up -d
```

Migrations run on start. Downgrading is not supported: to go back, run the older image and restore the backup taken before the upgrade.
