# Versions and rollback

Every publication creates a new version (a release); older versions are kept, and switching between them is instant.

- list_deploys shows the versions of a site, newest first, with the one being served marked as current, its date, size, number of files and where it came from (web interface, CLI, API, AI). If previews are enabled, each version also has its own link that always shows exactly that version: useful to let the user look before switching.
- "Bring back the previous version" = rollback without `deploy_id`: the version before the current one is served again. To go to a specific one, show the user a short list ("12 Nov, 14:05 — from the chat; 10 Nov — from the web interface") and pass the chosen `deploy_id`.
- Rollback does not delete anything: the newer version stays in the list and you can switch back.
- After rollback, a later edit through update_site_files must start from the version now being served (read it again).

Reply: "The previous version is back: <link>".
