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

Forms must be posted from the same site: a request whose `Origin` is another site gets 403. No CORS is needed or offered. `GET /_form/...` is always 404.

### Altcha (anti-robot check)

[Altcha](https://altcha.org) is a self-hosted proof of work: the visitor's browser spends a moment solving a puzzle, no third party is contacted and nothing is tracked. The platform serves the widget itself (`/_dropsite/altcha.js`, altcha 3.5.0, MIT) and issues puzzles at `/_form/challenge`. Add both to every form, as in the example above: while the owner has not enabled the check the field is ignored, so the widget is harmless.

When the owner turns on **Require Altcha** for the site, a submission without a valid, unused solution for this site address is rejected (400, `form_captcha_invalid`). Puzzles expire after 15 minutes. A normal visitor waits under a second on a computer and a few seconds on a phone; every further puzzle requested from the same IP for the same site within 10 minutes is 4 times harder, up to 256 times, and rejected posts raise the level at once. The level falls back as time passes.

The widget shows its status in Russian or English according to `<html lang>` or the browser language. Its attributes are documented on altcha.org (`auto="onsubmit"` starts solving when the form is submitted, `display="invisible"` hides it).

## For site owners

Open the site in the web interface: the **Submissions** block shows the channels and the latest submissions; **Channels and all submissions** opens the full screen.

- **Telegram.** Leave the chat ID empty and press *Add Telegram*: you get a `t.me/<bot>?start=<code>` link; open it and press *Start* in the chat that should receive submissions (for a group, add the bot to the group first and send the `/start <code>` command there). Alternatively enter a chat ID: the bot sends a 6-digit code to that chat, enter it on the screen. The bot can only write to chats where it was started or added.
- **E-mail.** Enter an address; a 6-digit code arrives by mail. Codes work for 30 minutes and 5 attempts.
- **Webhook.** Enter an `https://` address. The signing secret is shown once (rotate it any time). Every submission is a `POST` with JSON:

  ```json
  {"id": "42", "site_id": "k3f9x2m7qa", "host": "promo.example.site", "form": "lead",
   "created_at": "2026-11-12T10:00:00Z", "fields": {"name": "Ann"}, "field_list": [{"name": "name", "value": "Ann"}]}
  ```

  and the header `X-Dropsite-Signature: sha256=<hex HMAC-SHA256 of the raw body with your secret>`. Verify it before trusting the body; use `id` to drop duplicates. Answer 2xx. Redirects are not followed; 408, 429 and 5xx are retried, other 4xx are final. Addresses in private, loopback, link-local and other reserved networks are refused, also when a public name resolves to them.

Delivery runs in the background: 8 attempts with growing pauses (30 s, 1 min, 2 min … about 2 hours in total). Each submission shows its status per channel: queued, delivered or failed with the last error. Submissions are stored even when no channel is connected. **Download CSV** exports up to 10 000 newest submissions; cells starting with `=`, `+`, `-` or `@` get a leading apostrophe so spreadsheets do not run them as formulas.

## For administrators

**Settings → Form submissions**:

- **Telegram bot token** from [@BotFather](https://t.me/BotFather); it is checked with Telegram when saved. One bot serves all sites. The bot binds chats through `getUpdates`, so do not set a webhook for it and do not use it in another program.
- **SMTP**: server, port, encryption (`STARTTLS` on 587, `TLS` on 465, or none for a local relay), optional username and password, sender address. Owners confirm recipient addresses with a code, so the server cannot be used to mail arbitrary people.
- **Keep submissions** (default 90 days) and the base **Altcha difficulty** (default 100 000).

The bot token, SMTP password and webhook secrets are encrypted with `/state/secrets.key` and never shown again after saving (only "set"). The key is not in backups: see [backup.md](backup.md#the-form-secrets-key).

Limits: 20 submissions per IP in 10 minutes, 200 per site per hour, 60 Altcha puzzles per IP in 10 minutes, 10 channels per site, 10 new channels per user per hour. Counters live in memory and reset on restart.

Environment (rarely needed): `TELEGRAM_API_URL` (Bot API address for a proxy), `FORM_WEBHOOK_ALLOW_CIDRS` (private networks webhooks may reach, plain `http://` allowed there; test stacks only).

Caddy forwards only `POST /_form/*` (body capped at 64 KiB), `GET /_form/challenge` and `GET /_dropsite/altcha.js` to `control`; while `control` is down forms answer 502 and sites keep working. These requests are not counted as page views in the statistics.
