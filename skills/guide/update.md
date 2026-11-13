# Change an existing site (also from a new chat)

You do not need the original chat or files: the server keeps the site, and you can read it back.

1. Find the site: list_sites. If there are several and it is not obvious which one the user means, show their names and links and ask. Remember the site ID for the rest of the chat.
2. See what is in it: list_site_files returns the files of the version being served and its `deploy_id`. Keep that `deploy_id`: it is the base of your change.
3. Read only what you need: read_site_files with the paths you will change (usually the HTML pages and the stylesheet). Large binary files (photos) do not need to be read to be replaced.
4. Change: update_site_files with
   - `base_deploy_id` = the `deploy_id` you read from;
   - the files to add or replace (full new content of each file);
   - the paths to delete, if any;
   - optional `fetch` entries to download images or fonts from the web into the site.
   Everything else stays as it was. The result is a new version; the previous one stays available for rollback.
5. Reply with the link and a one-line summary of what changed ("New photos on the main page and the updated price list are live: <link>").

Conflicts: if the site changed after you read it (someone published from the web interface, another chat or the CLI), update_site_files refuses with a conflict. Do not retry blindly: read the files again with list_site_files and read_site_files, apply your change to the fresh content, and send it with the new `base_deploy_id`. Tell the user only if their own change was overwritten in a way they should check.

Typical requests:

- "Change the photos": ask for the new photos (or links to them) if the user has them; use `fetch` for links, file content for uploads. Keep the same file names only if you also bump a version in the name or the URL (`hero-2.webp`), because images are cached for an hour.
- "Change the prices", "fix the phone number": if get_site lists a collection with these items under `attached`, change the documents (`data`; if the collection is linked to a Google Sheet, the user edits the sheet, see `sheets`); otherwise edit the HTML text only.
- "Add a page": add the file and a link to it in the navigation of every page.
- Bigger rework of many files or many photos: use an upload session in patch mode, see `large_sites`.

Always change the site the user means. If they gave a link, match it with the `url`/`urls` from list_sites.
