# External domains (stage 2)

Not available in this version. Planned behaviour:

- A site owner adds their own domain (for example `promo.example.com`) to a site.
- They point the domain at the server with an `A`/`AAAA` record (or a `CNAME` to the site address).
- An administrator confirms the domain; only confirmed domains get a certificate and serve the site.
- Unconfirmed domains never get a certificate, so nobody can make the server request certificates for arbitrary names.

The data model already has the `external` host kind and the `verified` flag; the API endpoint `POST /sites/{id}/hostnames` arrives with stage 2.
