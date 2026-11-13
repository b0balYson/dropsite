# API keys: calling other services from a site

A static page cannot keep a secret: anything in its HTML or JavaScript is visible to every visitor. When a page needs a third-party API that requires a key, use a secret proxy endpoint of the site instead of putting the key in the code and instead of writing a backend. Typical cases:

- a chat assistant or text generator on the site (OpenAI or another model API);
- sending leads or orders to a CRM or a Telegram bot from the page's own JavaScript;
- maps, weather, delivery cost, currency rates and other APIs with a key.

For plain contact forms use the forms of the site (topic `forms`), not the proxy. For sign-in or a database the page talks to the user's own backend with its public key (topic `external_backend`).

## Pick the class

Every endpoint has a class with built-in protection. Pick the narrowest one:

- `ai` (the default for a new endpoint): a model API. The server sends only your **template**: the model, the system prompt, `max_tokens` and everything else are fixed; the visitor sends only the fields you declare. Needs the invisible visitor session (below). Default limits: 20 calls a minute per visitor, 50 per visitor session, 1000 a day.
- `send`: a message to a recipient fixed in the template, e.g. a Telegram bot with a fixed `chat_id` or a lead to a CRM. Session plus a fresh invisible anti-robot check for every call. Default limits: 5 a minute, 100 a day.
- `read`: GET-only data (maps, weather, rates). No session; 60 a minute per visitor, 10 000 a day.
- `transparent`: forwards any allowed method and path, like a plain proxy. Anyone who opens the site can then use the key for anything those methods and paths allow. Use it **only** on a password-protected site and only if the user explicitly wants it; set `class: "transparent"` explicitly. Existing endpoints from before classes are transparent: offer to switch them.

The daily budget (`daily_budget`) pauses the endpoint until midnight UTC when it runs out; the owner sees it on the API keys screen and gets one message a day through the site's Telegram or e-mail form channel, if set up. Raise `per_minute`, `daily_budget` or `session_quota` only when the user asks; 0 keeps the defaults.

## Template (ai and send)

```json
{
  "path": "/chat/completions",
  "body": {
    "model": "gpt-4o-mini",
    "max_tokens": 400,
    "messages": [
      {"role": "system", "content": "You answer questions about the bakery Pyshka. Be brief."},
      {"role": "user", "content": "{{question}}"}
    ]
  },
  "fields": {"question": {"type": "string", "max_length": 1000, "required": true}},
  "response": "choices.0.message.content"
}
```

- `body` is the exact JSON sent upstream. `"{{name}}"` marks where a visitor value goes: a string that is exactly one placeholder becomes the typed value; inside longer text the value is inserted as text (`"text": "New lead: {{name}}, {{phone}}"`).
- `fields` are the only values a visitor may send: `string` (with `max_length`), `number`, `integer`, `boolean`, or `messages` (a chat history: a list of `{role: user|assistant, content}`; a visitor can never send a `system` message). Every field must appear in `body`. Anything else in the request is refused.
- `response` is a dotted path into the answer (`choices.0.message.content`, Anthropic `content.0.text`); the page gets `{"result": <value>}` and nothing else. Without it the whole answer passes through.
- `stream: true` (with `"stream": true` in `body` too) forwards a server-sent event stream; with `response` (e.g. `choices.0.delta.content`) each event becomes `data: "<text piece>"`, ending with `data: [DONE]`.
- A Telegram bot: `upstream_url` `https://api.telegram.org/bot{secret}` with `auth: "path"`, class `send`, template `{"path": "/sendMessage", "body": {"chat_id": 123456789, "text": "From the site: {{text}}"}, "fields": {"text": {"type": "string", "max_length": 1000, "required": true}}, "response": "ok"}`.
- Templates contain no code or expressions; the server only substitutes values.

## Steps

1. Create the endpoint with set_proxy_endpoint: a short `name` (`openai`, `crm`, `tg`), the https base address in `upstream_url`, how the key is sent in `auth` (`bearer` for most model APIs, `header` with `auth_name` such as `x-api-key`, `query` with `auth_name`, or `path` with `{secret}` in the address), the `class` and, for `ai` and `send`, the `template`. For `read` and `transparent` keep `methods` and `paths` as narrow as the page needs.
2. Give the user the link from `secret_url` and ask them to open it and paste the key there themselves. **Never ask for the key in the chat, never accept it if the user sends it** (tell them to remove it and paste it on that page instead), and never write a key into the site's files or into `upstream_url`. There is no tool that sets a key: only the user can, in the web interface.
3. In the site's code load the session script once and call the endpoint with `dropsite.fetch` and a relative path, without any key:

```html
<script src="/_dropsite/session.js"></script>
<script>
async function ask(question) {
  const res = await dropsite.fetch('/_dropsite/proxy/openai', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ question }),
  });
  if (!res.ok) throw new Error('unavailable'); // show a friendly message
  const { result } = await res.json();
  return result;
}
</script>
```

   `session.js` solves a small proof of work in the background as the page loads (no widget, no click) and renews the session by itself. For `read` endpoints plain `fetch('/_dropsite/proxy/<name>/<path>')` works too. Always handle errors in the page: until the key is pasted every call answers 503 with the code `proxy_secret_missing`.
4. After publishing, check with list_proxy_endpoints that `secret_set` is true and there is no `warning`; its usage counters show forwarded, failed and refused calls and today's calls against the budget.

## What to tell the user

- Advise a key with minimal rights and a spending limit set at the API provider even so: the limits above slow abuse down but the provider's limit is the last line.
- Changing `upstream_url`, `allowed_hosts`, `auth` or `auth_name` removes the stored key, so the user must paste it again; changing the template, class, limits, methods, paths or turning the endpoint off and on keeps it.
- On a password-protected site the proxy works only for visitors who signed in.
- Limits: 10 endpoints per site, 1 MB per request, 10 MB per answer, 60 seconds per call, 1000 calls an hour per site. Calls from other websites are refused; requests from big clouds (AWS, Google Cloud, Azure, Oracle) and AI crawlers are blocked unless the administrator turned that protection off.
- delete_proxy_endpoint removes the endpoint and its key; ask for confirmation first.

## Errors from the proxy

The page receives JSON `{"error": {"code", "message"}}`: `proxy_secret_missing` (no key yet, send the user to `secret_url`), `proxy_session` or `proxy_pow` (the page does not use `dropsite.fetch` and `session.js`), `proxy_field` (the page sent a field the template does not declare, or a wrong type or length), `proxy_budget` (daily budget used up), `proxy_path` or `proxy_method` (widen `paths` or `methods` if the page really needs it), `proxy_origin` (called from another site), `rate_limited`, `proxy_too_large`, `proxy_upstream` (the API is unreachable or redirected elsewhere), `proxy_upstream_status` (the API refused a templated call, e.g. 401 for a wrong key or 400 for a wrong model name) and `proxy_timeout`. With `transparent` and `read` the API's own answers (for example 401 for a wrong key) pass through unchanged: a 401 usually means the user pasted a wrong or revoked key.
