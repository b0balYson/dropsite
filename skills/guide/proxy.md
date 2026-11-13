# API keys: calling other services from a site

A static page cannot keep a secret: anything in its HTML or JavaScript is visible to every visitor. When a page needs a third-party API that requires a key, use a secret proxy endpoint of the site instead of putting the key in the code and instead of writing a backend. Typical cases:

- a chat assistant or text generator on the site (OpenAI or another model API);
- sending leads or orders to a CRM, a spreadsheet API or a Telegram bot from the page's own JavaScript;
- maps, weather, delivery cost, currency rates, payment links and other APIs with a key.

For plain contact forms use the forms of the site (topic `forms`), not the proxy. For sign-in or a database the page talks to the user's own backend with its public key (topic `external_backend`).

## How it works

The page calls a relative address on its own site, `/_dropsite/proxy/<name>/<path>`. dropsite forwards the request to the endpoint's base address plus `<path>`, adds the stored key and returns the answer. The key never reaches the page, the chat or the API responses.

1. Create the endpoint with set_proxy_endpoint: a short `name` (for example `openai`, `crm`, `tg`), the https base address in `upstream_url`, and how the key is sent in `auth`:
   - `bearer` sends `Authorization: Bearer <key>` (OpenAI and most model APIs);
   - `header` sends a header named `auth_name`, for example `X-Api-Key`;
   - `query` adds a query parameter named `auth_name`, for example `api_key`;
   - `path` puts the key where `{secret}` stands in the base address, e.g. `https://api.telegram.org/bot{secret}` for a Telegram bot.
   Keep `methods` and `paths` as narrow as the page needs, e.g. methods `[POST]` and paths `[/chat/completions]`.
2. Give the user the link from `secret_url` and ask them to open it and paste the key there themselves. **Never ask for the key in the chat, never accept it if the user sends it** (tell them to remove it and paste it on that page instead), and never write a key into the site's files or into `upstream_url`. There is no tool that sets a key: only the user can, in the web interface.
3. In the site's code call the endpoint with a relative path and no key:

```js
const res = await fetch('/_dropsite/proxy/openai/chat/completions', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ model: 'gpt-4o-mini', messages: [{ role: 'user', content: text }] }),
});
if (!res.ok) { /* show a friendly message: the service is unavailable */ }
const data = await res.json();
```

   Streamed answers (`text/event-stream`) pass through as they arrive. Always handle errors in the page: until the key is pasted every call answers 503 with the code `proxy_secret_missing`.
4. After publishing, check with list_proxy_endpoints that `secret_set` is true; its usage counters show forwarded, failed and refused calls.

## What to tell the user

- Anyone who opens the site can make the page call the API, within the allowed methods and paths and the rate limits (60 calls a minute per visitor, 1000 an hour per site). Advise a key with minimal rights and a spending limit set at the API provider, and narrow paths.
- Changing `upstream_url`, `allowed_hosts`, `auth` or `auth_name` removes the stored key, so the user must paste it again; changing methods, paths or turning the endpoint off and on keeps it.
- On a password-protected site the proxy works only for visitors who signed in.
- Limits: 10 endpoints per site, 1 MB per request, 10 MB per answer, 60 seconds per call. Calls from other websites are refused.
- delete_proxy_endpoint removes the endpoint and its key; ask for confirmation first.

## Errors from the proxy

The page receives JSON `{"error": {"code", "message"}}`: `proxy_secret_missing` (no key yet, send the user to `secret_url`), `proxy_path` or `proxy_method` (widen `paths` or `methods` if the page really needs it), `proxy_origin` (called from another site), `rate_limited`, `proxy_too_large`, `proxy_upstream` (the API is unreachable or redirected elsewhere) and `proxy_timeout`. Answers of the API itself (for example 401 for a wrong key) pass through unchanged: a 401 usually means the user pasted a wrong or revoked key.
