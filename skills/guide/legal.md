# Personal data, privacy policy and cookies

A form that collects names, phones or e-mails collects personal data. In Russia this falls under 152-FZ, in the EU under the GDPR. Do the basics on every site with a form or analytics; you are not a lawyer, so say that the texts are a template the user should check.

- **Consent checkbox** in every form with personal data: a required, unchecked `<input type="checkbox" name="consent" required>` with a label like "I agree to the processing of my personal data under the privacy policy", where "privacy policy" links to the policy page. Never pre-check it. Russian sites: a separate consent is needed for marketing mailings.
- **Privacy policy page** (`privacy.html`), linked from every form and the footer: who processes the data (the business name, tax number, contacts), what data is collected and why, where it goes (Dropsite stores requests and forwards them to the owner's channels), how long it is kept, how to ask for deletion. Ask the user for the business details; never invent them.
- **Cookie banner** only when analytics counters are on (Yandex Metrica, Google Analytics set through set_counters): a small banner at the bottom saying the site uses cookies for statistics, with an "OK" button that hides it and remembers the choice in `localStorage`. For EU visitors, counters should start only after consent; tell the user this if their audience is in the EU. Dropsite's own visit statistics use no cookies and need no banner.
- Keep forms minimal: ask only for the data the business really needs.
