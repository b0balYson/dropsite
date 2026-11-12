After publishing a site with a form, tell the user to connect Telegram, e-mail or a webhook in the **Submissions** block of the site screen in the web interface.
If the server refuses the files because they contain secret keys (`secrets_found` with a list of file:line), remove those keys from the site, keep only public keys, and tell the user to rotate the exposed keys.
