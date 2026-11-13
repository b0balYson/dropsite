# Quizzes and calculators

Lead quizzes ("pick a kitchen in 5 questions") and calculators (price, delivery, loan) work on Dropsite as plain JavaScript in the page; the result arrives as an ordinary form submission.

- Build it as one `<form action="/_form/quiz" method="post">` with steps (`<fieldset>` per question, shown one at a time), real `<input type="radio">`, `<input type="checkbox">` and `<input type="range">` with labels, "Back" and "Next" buttons and a progress indicator. Without JavaScript all questions show at once and the form still sends.
- Keep the script small and in a site file (`js/quiz.js`, loaded with `defer`); no frameworks or quiz builders from other sites.
- The last step asks for contacts (name, phone or messenger), the consent checkbox (see "Personal data, privacy policy and cookies"), the honeypot and the Altcha widget as in every form. Send every answer as its own named field (`area`, `style`, `budget`), and put a calculated result in a hidden field (`<input type="hidden" name="estimate">`), so the owner sees the answers and the estimate in the request.
- Show the result on the page (for example "About 120 000–150 000 ₽") and say clearly that it is an estimate and the final price comes after contact.
- Prices and formulas come from the user; never invent them. Keep them in one place at the top of the script so the user can ask you to change them later.
- Offer to connect Telegram so quiz requests arrive at once.
