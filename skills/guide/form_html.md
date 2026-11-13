# Forms on the site

Dropsite receives form submissions itself: never use Formspree, Google Forms, Netlify Forms or other form services for a site published here, and never write a backend. Every form posts to `/_form/<name>` on the same site.

Generate forms like this, always with the honeypot and the Altcha widget (the widget is harmless until the owner turns the check on):

```html
<form action="/_form/lead" method="post">
  <label>Name <input name="name" autocomplete="name" required></label>
  <label>Phone <input name="phone" type="tel" autocomplete="tel" required></label>
  <input name="_hp" tabindex="-1" autocomplete="off" aria-hidden="true"
         style="position:absolute;left:-10000px">
  <input type="hidden" name="_next" value="/thanks.html">
  <altcha-widget challenge="/_form/challenge"></altcha-widget>
  <button type="submit">Send</button>
</form>
<script async defer src="/_dropsite/altcha.js" type="module"></script>
```

- `<name>` in `/_form/<name>` is `a-z`, `0-9`, `-`, `_` (for example `lead`, `callback`, `order`); use different names for different forms.
- `_hp` is a honeypot: keep it empty and visually hidden (not `type="hidden"`). Fields starting with `_` are never delivered.
- `_next` is a path on the same site shown after sending; create that page (for example `thanks.html`). Without `_next` a neutral built-in "Thank you" page is shown.
- No file inputs: files are dropped and bodies over 64 KB are rejected. At most 50 fields.
- With JavaScript, post with `fetch("/_form/lead", {method: "POST", headers: {Accept: "application/json"}, body: new FormData(form)})`; the answer is `{"ok": true}` or `{"ok": false, "error": {"code", "message"}}`.
- Load `/_dropsite/altcha.js` from the site itself, never from a CDN. Do not add other captchas.
- Forms work only on the site's own address (posts from other sites are refused). On a password-protected site only visitors who signed in can send the form.
