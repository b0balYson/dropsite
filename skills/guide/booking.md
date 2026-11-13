# Booking and appointments

Dropsite does not keep a calendar and does not count free seats or time slots. Say this plainly when the user asks for booking, and offer one of two ways:

- **The user's booking service** (YCLIENTS, Calendly, Cal.com, Google Calendar appointment pages, a restaurant booking system): ask for their booking link and put it on a clear "Book" button as an ordinary link. Embed their widget only if the user asks for it (see "Maps, videos and chat widgets").
- **A booking request form**: a form posting to `/_form/booking` with name, phone, preferred date and time (`<input type="date">`, `<input type="time">` or a short list of slots) and a comment. Say on the page that this is a request and that the business confirms it by phone or message; the thanks page repeats it. Offer to connect Telegram so requests arrive at once.

Never promise that a slot is reserved or that two people cannot pick the same time when the site only sends a request.
