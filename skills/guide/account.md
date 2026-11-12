# Account, limits, connections, administration

- get_account shows the user's profile, role and limits with current usage: number of sites and the maximum, maximum site size, and similar. Check it before a big upload or when creating a site fails because of a limit.
- update_site changes a site's name (title) and/or memorable address. delete_site deletes a site with all versions and addresses: confirm first, it cannot be undone.
- list_connections lists the apps (chat clients) and access keys connected to the account, without secrets, and marks the current connection. revoke_connection disconnects one after the user confirmed. Disconnecting the current connection ends this chat's access.
- You cannot create access keys or connect codes, and you never need them. New connections are made by the user on the "Connect AI" page of the dropsite web interface.

## What only people do (say it honestly)

- Connecting dropsite to a chat or computer the first time (Connect AI page).
- DNS records of their own domains, at their DNS provider; approval of a domain by an administrator.
- Confirming a delivery channel: pressing Start in Telegram, reading the e-mail code.
- Installation settings by an administrator in the web interface: the Telegram bot, the mail server, user accounts and limits, storage periods. Through the chat even an administrator manages only their own sites; the only installation-level action available is the root domain (see `addresses`).
