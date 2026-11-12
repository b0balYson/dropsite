# Payments

A site published with dropsite never handles card data: no fields for card numbers, CVC or expiry dates, and no payment code of your own.

- Use the payment link of the user's payment service: YooKassa (payment links or a payment form link), CloudPayments (payment link or widget page from their account), Stripe Payment Links, or similar. Ask the user to create the link (one per product or price) in their account and send it to you; never invent one.
- Put the link on a clear button ("Pay 1 500 ₽", "Buy now") as an ordinary `<a href>`; the payment page opens on the service's site. Set the return address in the service to a page of the site (for example `thanks.html`).
- When the price depends on the order (size, quantity, delivery), add an order form that posts to `/_form/order` with the customer's contacts and choices, and tell the user that they send the payment link after confirming the order. Offer to connect Telegram or e-mail so orders arrive at once.
- Show prices, what is included, delivery and refund terms, and the seller's details (name, tax number, contacts) next to the button: payment services and the law require them.
- Never ask for secret keys of a payment service and never put them into the site (Stripe `sk_live_…` keys are refused at publication).
