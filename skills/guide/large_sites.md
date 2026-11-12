# Sites over 16 MB: upload sessions

One deploy_files or update_site_files call carries at most 16 MB of file content (send a few MB per call: chat replies have their own size limits). For bigger sites use an upload session; it ends in one new version, so visitors never see a half-uploaded site.

1. begin_upload: name the existing site (or ask for a new site with a title) and the mode:
   - `replace`: the session becomes the whole new site; files you do not upload are gone.
   - `patch`: start from the version you read (`base_deploy_id`); only uploaded files change, and you can delete paths at commit.
   It returns an `upload_id`.
2. upload_files: send files in batches of a few MB of content per call (16 MB at most) (text as is, binary as base64), and/or `fetch` entries the server downloads itself. Repeat until everything is sent. Sending the same path again replaces it.
3. commit_upload: with the `upload_id` (and the paths to delete in patch mode). It checks the result like any other publication (index.html, limits) and returns the link.

Notes:

- A session expires if left unfinished; start a new one if commit says it is gone.
- The site size limit of the user still applies (see get_account); usually 50 MB unpacked and 20 MB per file. Shrink images before uploading (see `assets`).
- If commit reports a conflict in patch mode, the site changed meanwhile: re-read it and start again from the new version.
- Tell the user only the outcome: "Uploaded 34 photos, the gallery is live: <link>".
