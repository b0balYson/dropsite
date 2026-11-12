# Copies, templates, publishing on a schedule and screenshots

## Start from a template

When the user wants a typical site (a café, a service business, an event, a portfolio, a page with links) and has no design yet, offer a template: list_templates, then create_site_from_template with the business name as `title` and the user's language as `lang`. The site is live at once with sample texts, prices and contacts. Then replace every sample with the user's real data (list_site_files, read_site_files, update_site_files) and ask for what you do not know. Never leave sample phone numbers or addresses on a site the user shares.

## Copy a site

"Make the same site for our second shop" = clone_site, then change the copy. Say in one line what is not copied: forms and where requests go, site data, own domains, the memorable address, password, lifetime and analytics counters. Set them up again on the copy if the user needs them. To change the existing site, do not copy it: edit it.

## Publish at a given time

"Put the sale online on Monday at 9" = prepare the new version without showing it, then schedule it:

1. begin_upload (mode `patch` with the current deploy_id to change a few files, or `replace` for the whole site), upload_files.
2. schedule_publish with that `upload_id` instead of commit_upload, and `publish_at` in the user's time zone with the offset, e.g. `2026-12-07T09:00:00+03:00`. Ask for the time zone if you do not know it; never guess UTC.

An older version can be scheduled too: schedule_publish with `site` and its `deploy_id` from list_deploys.

Tell the user the date and time in their words, that the site keeps showing the current version until then, and give the preview link if one was returned. list_scheduled_publishes shows what is waiting and what happened; cancel_scheduled_publish cancels a waiting one (the prepared version stays in the list). A site that is deleted or expires before the time is not published.

## Check your work with screenshots

After building or changing a site, call screenshot_site (desktop and mobile) and look at the picture before you say it is ready: text that overflows, unreadable contrast, missing images, a broken layout on the phone. Fix what you see. compare_versions shows "before | after" with changes highlighted and the share of changed pixels: use it to show the user what changed, or to make sure an edit touched only what was asked.

Screenshots show the first screen only and are taken from the site's own files, so anything loaded from other servers is missing on them. If the server answers that screenshots are not available (`renderer_unavailable`), do not retry: say once that previews are not enabled on this server and give the link to open.
