## Publishing static sites with dropsite

When the user asks to publish, deploy, share or "put online" a page or site from this project, publish it with the `dropsite` CLI and give them the link.

1. Check that the CLI is ready: `dropsite version && dropsite sites --json >/dev/null && echo READY`.
   - If `dropsite` is not found, stop and ask the user to install it from the **Connect AI** page of their dropsite web interface.
   - If the error mentions `dropsite login` or `401`, stop and ask the user to issue a token on that page and run the shown `dropsite login` command in their own terminal. Never ask for the token in the chat.
2. Choose the folder: it must contain `index.html` at its root. Build the project first if it has a build step and publish the output (`dist/`, `build/`, `out/`). Never publish a folder with source code, `.env` files or secrets.
3. Publish: `dropsite deploy <folder> --json`. The first run creates a site and writes `.dropsite.json` into the folder; later runs update the same site. Use `--site <id-or-slug>` to target an existing site and `--title "<name>"` to name a new one.
4. Reply with the `url` from the JSON output as a link. Mention that publishing again updates the same address and that `dropsite rollback <site>` restores the previous version.

If the CLI reports a missing `index.html`, a size or file-count limit, a symbolic link or a rate limit, explain the message to the user and fix the folder before retrying once.

### Forms on sites published with dropsite

dropsite receives form submissions itself: do not use third-party form services or write a backend. Post every form to `/_form/<name>` on the same site with `method="post"`, and always include a visually hidden honeypot `<input name="_hp" tabindex="-1" autocomplete="off" aria-hidden="true" style="position:absolute;left:-10000px">`, an optional `<input type="hidden" name="_next" value="/thanks.html">` (a page of the same site shown after sending), the Altcha widget `<altcha-widget challenge="/_form/challenge"></altcha-widget>` inside the form and `<script async defer src="/_dropsite/altcha.js" type="module"></script>` on the page. No file inputs (bodies over 64 KB are rejected). With JavaScript, `fetch` the same URL with `Accept: application/json` and read `{"ok": true}`. After publishing, tell the user to connect Telegram, e-mail or a webhook in the **Submissions** block of the site screen in the web interface.
