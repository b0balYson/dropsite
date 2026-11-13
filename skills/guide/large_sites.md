# Sites over 16 MB: upload sessions and the upload link

One deploy_files or update_site_files call carries at most 16 MB of file content, and in practice much less: you cannot write millions of characters of base64 into a tool call. A site with many photos or animation frames goes through an upload session; it ends in one new version, so visitors never see a half-uploaded site.

1. begin_upload: name the existing site (or ask for a new site with a title) and the mode:
   - `replace`: the session becomes the whole new site; files you do not upload are gone.
   - `patch`: start from the version you read (`base_deploy_id`); only uploaded files change, and you can delete paths at commit.
   It returns `upload_id`, `upload_url`, `upload_command` and `next_steps`.
2. Send the files through `upload_url`, not through the chat:
   - **You can run shell commands** on the computer that has the files (Claude Code, Codex, a terminal tool): run `upload_command` with `<folder>` replaced by the site folder, the one with `index.html`:
     `tar -C <folder> -cf - . | curl -sS --data-binary @- -H 'Content-Type: application/x-tar' '<upload_url>'`
     It answers with JSON: `received`, `skipped` (secret-looking files such as `.env` or keys are never taken) or an `error` to fix. Run it again after fixing; a path sent again replaces the file. One file: `curl -sS -T <file> '<upload_url>/<path>'`.
   - **You cannot run commands** (ChatGPT, claude.ai, a phone): give the user the link in plain words: "Open this link, drag the site folder (or the files) onto the page, and when it says Done, come back and write: done". Then wait for their reply.
   - Files you wrote yourself, or a few small ones, can still go through upload_files (a few MB of content per call); `fetch` entries download public https addresses.
3. commit_upload: with the `upload_id` (and the paths to delete in patch mode). It checks the result like any other publication (index.html, limits, secrets) and returns the link.

Rules:

- Never use tunnels (ngrok, localtunnel, cloudflared and the like), file hosting, cloud drives or other servers to move the files, and never ask the user to install anything for this. The upload link is the way.
- The link works for one hour from its last use and only for this upload; it gives no access to the account. If the user says the link expired, call begin_upload again and give the new link.
- The site size limit of the user still applies (see get_account); usually 50 MB unpacked and 20 MB per file. Shrink images before uploading (see `assets`).
- A session expires if left unfinished for 24 hours; start a new one if commit says it is gone.
- If commit reports a conflict in patch mode, the site changed meanwhile: re-read it and start again from the new version.
- Tell the user only the outcome: "Uploaded 34 photos, the gallery is live: <link>".
