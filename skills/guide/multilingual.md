# Sites in several languages

- Keep the main language at the root (`index.html`, `about.html`) and every other language in its own folder with the same file names: `/en/index.html`, `/en/about.html`. Do not switch languages with JavaScript on one page: search engines and link previews see only one language then.
- Every page sets its language (`<html lang="ru">`, `<html lang="en">`) and lists all its versions in `<head>`: `<link rel="alternate" hreflang="ru" href="<full address>/about.html">`, `<link rel="alternate" hreflang="en" href="<full address>/en/about.html">` and `hreflang="x-default"` pointing to the main version.
- A language switcher in the header: plain links to the same page in the other language (`RU` / `EN`), the current one marked with `aria-current="page"`. Never redirect visitors automatically by browser language.
- Translate everything the visitor sees: titles, descriptions, `alt` texts, buttons, form labels, the thanks page and `404.html`. Forms in every language may post to the same `/_form/<name>`; add a hidden field `lang` so the owner knows which version the request came from.
- List every language version in `sitemap.xml` (see "Search engines and link previews").
- Ask the user which languages they need and who checks the translation; offer your translation as a draft.
