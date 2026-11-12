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

## Upgrade

```bash
docker compose exec -T control /control backup - > before-upgrade.tar.gz
docker compose pull && docker compose up -d
```

Migrations run on start. Downgrading is not supported: to go back, run the older image and restore the backup taken before the upgrade.
