## Where requests go

Every submission is stored first, even when no channel is connected, and kept for a period set by the administrator (90 days by default). Then it is delivered in the background, with retries, to the site's channels.

- See the channels: list_channels. Add one with add_channel:
  - **Telegram**: add_channel returns a link to the dropsite bot. Tell the user: "Open this link and press Start in the chat where requests should arrive" (for a group: add the bot to the group first, then send the command the link shows). Alternatively, with a chat ID the bot sends a 6-digit code to that chat; ask the user for the code and pass it to verify_channel.
  - **E-mail**: a 6-digit code is sent to the address. Ask the user for it and call verify_channel. Codes work 30 minutes, 5 attempts.
  - **Webhook** (for developers and CRM integrations): an https address; the signing secret is shown once in the tool result: pass it on to the user once, tell them to store it, and never repeat it later. rotate_channel_secret issues a new one.
  The channel delivers only after it is confirmed. Check with list_channels and tell the user when it works.
- Turn a channel off or on with set_channel_enabled; a webhook that kept failing gets paused and is resumed the same way after the receiver is fixed. Delete with delete_channel only after the user confirmed.
- If the installation has no Telegram bot or mail server configured, add_channel says so: tell the user that their dropsite administrator has to set it up, and offer another channel.
- Form settings (for example requiring the anti-robot check Altcha): get_form_settings and set_form_settings. get_form_settings also returns form_snippet, a ready form for this site; adapt its fields instead of writing one from scratch. Turn Altcha on when the user complains about spam; the widget is already on the page if the form was built as above.

## Reading requests

- list_submissions shows the newest requests, with filters by form name, period and text, and the delivery status of each. Show them as a short readable list: date, form, the fields that matter (name, phone, message). Do not dump raw data.
- export_submissions gives a CSV of a limited size. For more, tell the user to press "Download CSV" on the site's requests screen in the web interface.
- "Show the latest requests" = list_submissions for the site, newest first, about 10.

After publishing a site with a form, offer to connect Telegram or e-mail.
