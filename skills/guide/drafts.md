# Drafts: show the change before it goes live

Every publication tool (deploy_files, deploy_folder, update_site_files, commit_upload) takes `draft: true`. A draft is built and checked exactly like a publication (secret check, optimization, quality report), but visitors keep seeing the current version. The result has `draft: true` and `preview_url`: one stable address per site (`p-<site id>.<base domain>`) that always shows the site's current draft, hidden from search engines and protected by the site's password if it has one.

## When to make a draft

- **The site is already live** — it has visits (get_site_stats), an own domain, or was created more than three days ago: make a draft by default. Reply "Here is how it will look: <preview_url>. Publish?" and call publish_draft only after a yes.
- **A brand-new site** (created in this chat, or no visits and only a few hours old): publish directly, there is nothing to protect.
- **The user says "publish directly", "no need to show me", "just do it"**: publish directly, and keep doing so for the rest of this chat without asking again.
- Small fixes the user asked for word by word (a typo, a phone number) on a live site: a draft is still the default, but keep the message short.

## Working with a draft

- The site has at most one draft. A new draft replaces the previous one (the old draft is deleted), so keep changing the same draft: list_site_files and read_site_files with `deploy_id` = the draft's `deploy_id` (get_site shows it under `draft`), then update_site_files with `base_deploy_id` = that `deploy_id` and `draft: true`. The preview link stays the same; tell the user to refresh it.
- "Publish", "looks good", "yes": publish_draft. It is instant; the previous version stays available for rollback.
- "Forget it", "delete the draft": discard_draft, after the user confirmed, with `confirm` = the site's slug or ID.
- A direct publication does not touch the draft; mention a leftover draft only if it matters.
- A draft waits for the user's "yes". To go live at a given time without asking again, prepare the version and schedule it instead (`workflow`).
- The first opening of a preview link can take a few seconds (the server prepares its secure connection).

## Live data on the preview

The preview is the same site: collections (/_dropsite/data), forms and API keys behind /_dropsite/proxy are the live ones. Say it in one line when the change touches them: "The preview uses your real data: a test request sent from it arrives like a real one, and changes to the menu data are visible on the live site at once." New collections or proxy endpoints a draft needs are created as usual (save_collection, set_proxy_endpoint) and are already live.
