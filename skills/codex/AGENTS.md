## Publishing static sites with pushpage

When the user asks to publish, deploy, share or "put online" a page or site from this project, publish it with the `pushpage` CLI and give them the link.

1. Check that the CLI is ready: `pushpage version && pushpage sites --json >/dev/null && echo READY`.
   - If `pushpage` is not found, stop and ask the user to install it from the **Connect AI** page of their pushpage web interface.
   - If the error mentions `pushpage login` or `401`, stop and ask the user to issue a token on that page and run the shown `pushpage login` command in their own terminal. Never ask for the token in the chat.
2. Choose the folder: it must contain `index.html` at its root. Build the project first if it has a build step and publish the output (`dist/`, `build/`, `out/`). Never publish a folder with source code, `.env` files or secrets.
3. Publish: `pushpage deploy <folder> --json`. The first run creates a site and writes `.pushpage.json` into the folder; later runs update the same site. Use `--site <id-or-slug>` to target an existing site and `--title "<name>"` to name a new one.
4. Reply with the `url` from the JSON output as a link. Mention that publishing again updates the same address and that `pushpage rollback <site>` restores the previous version.

If the CLI reports a missing `index.html`, a size or file-count limit, a symbolic link or a rate limit, explain the message to the user and fix the folder before retrying once.
