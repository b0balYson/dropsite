After publishing a site with a form, tell the user to open the site in the Dropsite web interface and connect a channel in the **Submissions** block (Telegram, e-mail or a webhook; optionally turn on **Require Altcha**); until then submissions are stored but not forwarded.

## Errors

The CLI prints the server message. Common ones:

| Message | What to do |
| --- | --- |
| no `index.html` | Publish the folder that contains `index.html`, or rename the main page |
| archive too large, too many files, file too large | Remove unused assets (videos, source images) or compress them, then retry |
| symbolic link | Replace symlinks with real files |
| secret keys found (`secrets_found`, a list of file:line) | Remove the secret keys from those files (use only public keys on a site) and tell the user to rotate them; `--include-sensitive` does not override this |
| site limit reached | Ask the user to delete an old site or contact the administrator |
| too many requests | Wait the time the CLI shows, then retry once |
