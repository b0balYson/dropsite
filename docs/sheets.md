# Google Sheets

dropsite connects Google Sheets in two ways:

- **Form submissions to a sheet**: a form delivery channel of kind `sheets` adds one row per submission.
- **Site data from a sheet**: a public data collection takes its documents from a sheet; the sheet is the source of truth.

Both work through one Google **service account** of the installation. Users share their spreadsheets with its address, the way they would share with a colleague.

## Administrator: connect the service account

1. In [Google Cloud Console](https://console.cloud.google.com/) create a project (or pick one) and enable the **Google Sheets API**.
2. In *IAM & Admin → Service accounts* create a service account. It needs no roles.
3. Open it, *Keys → Add key → Create new key → JSON*, and download the file.
4. In dropsite open *Settings → Google Sheets*, paste the whole file and save.

dropsite checks the key by asking Google for an access token, stores it encrypted with `state/secrets.key` (like the form secrets) and shows only the service account address afterwards. The key is never returned by the web interface, the API or the MCP tools, and it cannot be set through the API or the chat. Removing the key turns every Google Sheets feature off; channels and links stay and report that Google Sheets is not configured.

### Checklist: what the service account may reach

The service account acts for every user of the installation: anyone who can name a spreadsheet the account can open is able to link it and read its rows through their own site, without having Google access to it themselves. Keep its reach as small as the feature needs:

- Create a **dedicated** service account in a **dedicated** Google Cloud project for this installation; do not reuse an account of other services.
- Give it **no IAM roles**, no domain-wide delegation and no Google Workspace admin roles; enable only the Google Sheets API in its project.
- Do not add it to **shared drives**, groups or folders: it should see only the spreadsheets users share with it one by one.
- Never share confidential spreadsheets with it, and ask users to share only the spreadsheets meant for their sites (Viewer for site data, Editor for forms).
- From time to time open *Shared with me* of the account (or the Drive audit log of your Workspace) and remove access to spreadsheets nobody uses any more; rotate the key if it may have leaked, and delete the old one in Google Cloud.

The same warning is shown next to the key in *Settings → Google Sheets*. Separate Google access per user is not available; see [security.md](security.md).

Google limits the Sheets API requests per minute of the project (see *APIs & Services → Google Sheets API → Quotas*); the quota is shared by the whole installation.

## Users: share a spreadsheet

Open the spreadsheet, press **Share** and add the service account address shown in the web interface (or by `get_sheets_setup` in the chat):

- **Editor** for form submissions (dropsite writes rows);
- **Viewer** is enough for site data.

Then copy the link of the sheet (tab) from the browser. Links look like `https://docs.google.com/spreadsheets/d/<id>/edit#gid=<number>`; `gid` picks the tab, without it the first tab is used. dropsite finds the tab by `gid`, so renaming a tab does not break anything.

Anyone who knows the link of a spreadsheet shared with the service account can connect it on this installation. Spreadsheet links are long random identifiers, but do not share confidential spreadsheets with the service account of a shared installation.

## Form submissions to a sheet

Add a channel of kind **Google Sheet** on the submissions screen of the site (API: `POST /sites/{ref}/forms/channels` with `{"kind": "sheets", "target": "<link>"}`; chat: `add_channel`). dropsite writes the header row as a test: if that works the channel is active at once, without a confirmation code. Read-only access is reported immediately.

- The first columns are the date (UTC, `YYYY-MM-DD HH:MM:SS`) and the form name (`Дата`/`Форма` or `Date`/`Form`, by the owner's language; both are recognized).
- Every field goes to the column whose header has the same text. A field without a column gets a new column on the right.
- Columns added, renamed or reordered by the owner are kept: matching is by header text.
- Values are written as plain text (`valueInputOption=RAW`): a value like `=IMPORTXML(...)` is stored as text and never evaluated.
- Deliveries use the common queue with retries. Access errors, Google limits and outages are temporary: submissions wait. After 10 failed attempts in a row the channel is paused (like a webhook) until the owner turns it on again.

## Site data from a sheet

On the page of a public collection (*Site data*), the **Google Sheet** block links the collection to a sheet (API: `PUT /sites/{ref}/collections/{name}/sheet`; chat: `link_sheet`). Linking synchronizes at once.

Rules of a synchronization:

- Row 1 holds the field names (trimmed). Columns with an empty header are skipped; a repeated name is an error of row 1.
- Every other non-empty row becomes a document, in the order of the rows. Empty cells are left out of the document.
- A column named `id` (any case) sets the document ID (`a-z`, `0-9`, `-`, `_`, up to 64 characters); otherwise the ID is `row-<number>`, which changes when rows move. A repeated ID is an error.
- With a collection schema values are converted to the property type: `number` and `integer` (spaces dropped, a comma is the decimal mark, `1,290.50` also works), `boolean` (`true`/`false`, `yes`/`no`, `да`/`нет`, `1`/`0`), `array` (comma-separated values). Without a schema every value is a string. Every document is then checked against the schema and the size limit.
- At most 5000 data rows and 100 columns.
- Any error leaves the documents unchanged; the result lists up to 50 errors with the row number, the column and the reason. Otherwise the collection's documents are replaced in one transaction.

The interval of periodic synchronization is 0 (only on request) or 5 to 1440 minutes. A background loop of `control` checks every minute. Manual synchronizations and links are limited to 30 per site and hour. Unlinking keeps the documents. Deleting the collection or the site removes the link.

### Why only one direction

For a linked collection the sheet is the source of truth. Edits made through the chat, the API or the web interface are allowed but overwritten by the next synchronization. Two-way synchronization would need conflict resolution (the same row changed in the sheet and in the chat), stable row identity without a key column, and writing into a document the marketer formats and rearranges by hand; a mistake there damages the user's working file. One direction keeps one simple rule: *change the sheet, the site follows*.

Exporting a collection to a sheet is not available.

## Security

- dropsite calls only `https://oauth2.googleapis.com` and `https://sheets.googleapis.com`: other hosts, other ports and redirects are refused. Connections go only to public addresses, checked on the connected socket (not to the host itself or private networks); no environment proxy is used.
- The `token_uri` of the key file is ignored: the signed token request always goes to Google.
- Every call is limited to 20 seconds and 10 MB of response; a whole synchronization to 60 seconds.
- Google's own error texts go to the server log only; users see short messages.
- Audit log: `sheets.settings.update` (without the key), `collection.sheet.link`, `collection.sheet.unlink`, `collection.sheet.sync` (with `trigger` manual or schedule), and the usual `form.channel.*` entries.
