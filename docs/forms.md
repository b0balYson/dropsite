# Forms and submissions

Every published site can receive form submissions on its own address. The platform stores each submission, protects the form from spam and forwards submissions to the channels the site owner connects: Telegram, e-mail or a webhook.

## For site authors

### A form without JavaScript

```html
<form action="/_form/lead" method="post">
  <label>Name <input name="name" required></label>
  <label>Phone <input name="phone" type="tel" required></label>
  <label>Message <textarea name="message"></textarea></label>

  <!-- honeypot: hidden from people, filled in by bots -->
  <input name="_hp" tabindex="-1" autocomplete="off" aria-hidden="true"
         style="position:absolute;left:-10000px">
  <!-- where to go after sending: a page of the same site -->
  <input type="hidden" name="_next" value="/thanks.html">
  <!-- anti-robot check, harmless while the owner has not turned it on -->
  <altcha-widget challenge="/_form/challenge"></altcha-widget>

  <button type="submit">Send</button>
</form>
<script async defer src="/_dropsite/altcha.js" type="module"></script>
```

- `action` is `/_form/<name>` on the same site. The name (1–64 characters `a-z`, `0-9`, `-`, `_`, case-insensitive) lets the owner tell forms apart: `lead`, `callback`, `order`.
- `method="post"`. Bodies may be `application/x-www-form-urlencoded` (the default), `multipart/form-data` or `application/json`.
- Files are not accepted: file inputs are dropped, and a body over 64 KB is rejected.
- Up to 50 fields; names up to 64 characters, values up to 10 000 characters. Repeated names (checkboxes) are joined with `, `.

After a successful post the browser is redirected (303) to `_next`, if it is a path on the same site (`/thanks.html`, `/ok?x=1`). Anything else — another site, `//host`, a full URL — is ignored and a neutral built-in "Thank you" page is shown with a link back to `/`.

### Service fields

Fields whose names start with `_` are never stored or delivered.

| Field | Meaning |
| --- | --- |
| `_hp` | Honeypot. Keep it empty and invisible to people (not `type="hidden"`: bots skip those). A filled honeypot is answered like a success, but nothing is stored. |
| `_next` | Same-site path to redirect a browser to after sending. |
| `_t` | Optional: the time the page was rendered, Unix seconds or milliseconds. A form posted less than 2 seconds later is silently dropped. Set it from a script, e.g. `form._t.value = Date.now()`. |
| `altcha` | Written by the Altcha widget; not stored. |

### Sending with fetch

```js
const res = await fetch("/_form/lead", {
  method: "POST",
  headers: { "Accept": "application/json" },
  body: new FormData(form),
});
const data = await res.json(); // {"ok": true} or {"ok": false, "error": {"code", "message"}}
```

JSON is returned to everything that is not a browser navigation (`Sec-Fetch-Mode` other than `navigate`, or no `text/html` in `Accept`). Error codes: `form_invalid`, `form_empty`, `form_too_large` (413), `form_unsupported_type` (415), `form_origin_mismatch` (403), `form_captcha_invalid`, `rate_limited` (429 with `Retry-After`), `not_found` (unknown site or bad form name).

Forms must be posted from the same site: a request whose `Origin` is another site, or that a browser marks `Sec-Fetch-Site: cross-site`, gets 403. No CORS is needed or offered. These checks only stop casual cross-site posting: clients without browser headers pass them, so the protection against spam is Altcha, the honeypot, the fill time and the rate limits. `GET /_form/...` is always 404.

### Altcha (anti-robot check)

[Altcha](https://altcha.org) is a self-hosted proof of work: the visitor's browser spends a moment solving a puzzle, no third party is contacted and nothing is tracked. The platform serves the widget itself (`/_dropsite/altcha.js`, altcha 3.5.0, MIT) and issues puzzles at `/_form/challenge`. Add both to every form, as in the example above: while the owner has not enabled the check the field is ignored, so the widget is harmless.

When the owner turns on **Require Altcha** for the site, a submission without a valid, unused solution for this site address is rejected (400, `form_captcha_invalid`). A puzzle is bound to its purpose: one issued for the sign-in page of a protected site (`/_dropsite/login/challenge`) is not accepted by forms, and vice versa. Puzzles expire after 15 minutes. A normal visitor waits under a second on a computer and a few seconds on a phone; every further puzzle requested from the same IP for the same site within 10 minutes is 4 times harder, up to 256 times, and rejected posts raise the level at once. The level falls back as time passes.

The widget shows its status in Russian or English according to `<html lang>` or the browser language. Its attributes are documented on altcha.org (`auto="onsubmit"` starts solving when the form is submitted, `display="invisible"` hides it).

## For site owners

Open the site in the web interface: the **Submissions** block shows the channels and the latest submissions; **Channels and all submissions** opens the full screen.

- **Telegram.** Leave the chat ID empty and press *Add Telegram*: you get a `t.me/<bot>?start=<code>` link; open it and press *Start* in the chat that should receive submissions (for a group, add the bot to the group first and send the `/start <code>` command there). Alternatively enter a chat ID: the bot sends a 6-digit code to that chat, enter it on the screen. The bot can only write to chats where it was started or added.
- **E-mail.** Enter an address; a 6-digit code arrives by mail (subject "dropsite: код подтверждения / confirmation code", the site address is in the body). Codes work for 30 minutes and 5 attempts. If the mail server fails, you see a general message; the administrator finds the server's reply in the control log.
- **Webhook.** Enter an `https://` address. The signing secret is shown once (rotate it any time). Every submission is a `POST` with JSON:

  ```json
  {"id": "42", "site_id": "k3f9x2m7qa", "host": "promo.example.site", "form": "lead",
   "created_at": "2026-11-12T10:00:00Z", "fields": {"name": "Ann"}, "field_list": [{"name": "name", "value": "Ann"}]}
  ```

  and the header `X-Dropsite-Signature: sha256=<hex HMAC-SHA256 of the raw body with your secret>`. Verify it before trusting the body; use `id` to drop duplicates. Answer 2xx within 10 seconds (8 seconds to the response headers). Redirects are not followed; 408, 429, 5xx and timeouts are retried, other 4xx are final. Addresses in private, loopback, link-local and other reserved networks are refused, also when a public name resolves to them.

  **Pause after failures.** After 10 failed attempts in a row the webhook channel is *paused*: nothing is sent to it, its queued submissions wait, and the channel shows "paused" with the time and the reason (the API returns `paused_at` and `consecutive_failures`). Fix the receiver and press **Resume** (API: `PATCH …/channels/{id}` with `{"enabled": true}`); queued submissions are then sent with the remaining attempts. A successful delivery resets the counter. Telegram and e-mail channels are not paused: their servers are shared by the whole installation.

Delivery runs in the background: 8 attempts with growing pauses (30 s, 1 min, 2 min … about 2 hours in total). Each submission shows its status per channel: queued, delivered, failed with the last error, or cancelled. Turning a channel off cancels its queued submissions (they are not sent when it is turned on again); deleting a site cancels everything queued for it. Submissions are stored even when no channel is connected.

The installation sends at most 8 deliveries at a time, at most 2 per site and 1 per channel, and takes sites in turn, so slow receivers of one site do not hold up the others.

**Download CSV** exports up to 10 000 newest submissions (and at most 64 MB; a larger export ends at the last whole row). Columns: `created_at`, `form`, `host`, then one column per field name — the 100 most frequent names of the export — and, if some names did not get a column, `_other` with the remaining fields of the row as a JSON object. Cells starting with `=`, `+`, `-` or `@` get a leading apostrophe so spreadsheets do not run them as formulas.

## From a chat assistant

The remote MCP server (`https://api.<BASE_DOMAIN>/mcp`, see [ai-clients.md](ai-clients.md)) gives ChatGPT, claude.ai and other MCP clients the same powers as the site's forms screen, with the same permission checks and limits:

| Tool | What it does |
| --- | --- |
| `get_form_settings` | Altcha flag, form names with submissions, the `/_form/<name>` address of the site, a ready HTML form (`form_snippet`, with `_hp` and the Altcha widget), whether Telegram and e-mail are set up on the server, the link to the forms screen |
| `set_form_settings` | turns **Require Altcha** on or off |
| `list_submissions` | newest first; filters `form`, `days` (last N days by the server clock), `since`/`until` (`YYYY-MM-DD` in UTC, `until` includes the whole day, or RFC 3339), `query` (text in field values; case-insensitive for Latin letters only); `limit` up to 100 and `cursor` for the next page; delivery state per channel in plain words |
| `export_submissions` | the same CSV as **Download CSV** returned as text: at most 1000 rows and 256 KB (default 200 rows, 100 KB), cut at a whole row; when truncated, the answer points to the forms screen for the full file |
| `list_channels` | channels with their state in plain words (waiting for confirmation, active, off, paused) |
| `add_channel` | Telegram without a chat ID returns the bot link the user must open and press *Start*; with a chat ID or for e-mail a 6-digit code is sent and the assistant asks the user for it; a webhook returns its signing secret once |
| `verify_channel` | confirms a channel with the code |
| `set_channel_enabled` | turns a channel off or on (on also resumes a paused webhook) |
| `delete_channel`, `rotate_channel_secret` | need `confirm` with the channel's ID or address after the user agreed |

Nothing is bypassed: 10 channels per site, 10 new channels per user per hour, 3 confirmation codes per recipient per day, 5 code attempts. If Telegram or e-mail is not set up on the installation, `add_channel` answers `channel_unavailable` and the assistant tells the user that the administrator must set it up. Installation settings (bot token, SMTP, retention) are not available through MCP. The local `dropsite mcp` server offers the same form tools through the API.

## For administrators

**Settings → Form submissions**:

- **Telegram bot token** from [@BotFather](https://t.me/BotFather); it is checked with Telegram when saved. One bot serves all sites. The bot binds chats through `getUpdates`, so do not set a webhook for it and do not use it in another program.
- **SMTP**: server, port, encryption (`STARTTLS` on 587, `TLS` on 465, or none for a local relay), optional username and password, sender address. Owners confirm recipient addresses with a code, so the server cannot be used to mail arbitrary people. Users and delivery statuses only show a general error ("the mail server refused the message", "…is unreachable"); the server's reply is written to the control log (`form channel confirmation e-mail failed`, `form delivery attempt failed`).
- **Keep submissions** (default 90 days) and the base **Altcha difficulty** (default 100 000).

The bot token, SMTP password and webhook secrets are encrypted with `/state/secrets.key` and never shown again after saving (only "set"). The key is not in backups: see [backup.md](backup.md#the-form-secrets-key).

Limits: 20 submissions per IP in 10 minutes, 200 per site per hour, 60 Altcha puzzles per IP in 10 minutes, 10 channels per site, 10 new channels per user per hour. Confirmation codes (e-mail and Telegram by chat ID): 3 per recipient per 24 hours and 100 per hour for the whole installation, so the platform cannot be used to send messages to strangers. Counters live in memory and reset on restart.

Environment (rarely needed): `TELEGRAM_API_URL` (Bot API address for a proxy), `FORM_WEBHOOK_ALLOW_CIDRS` (private networks webhooks may reach, plain `http://` allowed there; test stacks only).

**`FORM_WEBHOOK_ALLOW_CIDRS` applies to every user.** Any user who can create a site can then point a webhook at any address in those networks and see the HTTP status in the delivery error: other containers of the compose stack, the Docker host (e.g. `172.17.0.1`), services of your LAN. The addresses of the control host itself (all its interfaces, loopback, `0.0.0.0`) are always refused, so its internal listener (`INTERNAL_ADDR`, used by Caddy's `ask` and the access check) and the admin listener cannot be reached even when an allowlisted network covers them. Keep the variable empty in production; on a test stack, list only the network of the test receiver.

Caddy forwards `POST /_form/*` (body capped at 64 KiB) and `GET /_form/challenge` to `control` after the password check of protected sites: a password-protected site accepts forms only from visitors who signed in to it. `/_dropsite/altcha.js` is served like the rest of `/_dropsite/*` and needs no sign-in. While `control` is down forms answer 502 and sites keep working. These requests are not counted as page views in the statistics.
