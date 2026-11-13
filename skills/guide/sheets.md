# Google Sheets: submissions to a sheet, site data from a sheet

Many users already keep their work in Google Sheets. Dropsite connects a sheet in two ways:

- **Form submissions to a sheet**: every submission of the site's forms is added as a row (leads, orders, sign-ups).
- **Site data from a sheet**: a public data collection (topic `data`) takes its documents from a sheet, so the user edits prices, a menu, a schedule or a catalog in the sheet and the site follows.

## Setup the user does

1. Call get_sheets_setup. If `configured` is false, tell the user that the administrator of the Dropsite server must add a Google service account key in the settings of the web interface; there is nothing else to do in the chat. **Never ask anyone for the key** and never accept it in the chat.
2. Otherwise tell the user, in plain words: open the spreadsheet in Google Sheets, press **Share**, add the address from `service_account_email` and choose **Editor** (needed for submissions; **Viewer** is enough for site data), then copy the link of the sheet (tab) from the browser address bar and send it. The link ends with `#gid=<number>`, which picks the tab; without it the first tab is used.

## Submissions to a sheet

Call add_channel with `kind: "sheets"` and the link in `target`. Dropsite writes the header row as a test, so the channel is active at once, with no code. Columns: the date (UTC) and the form name first, then one column per form field; a field the sheet has no column for gets a new column on the right. The user may rename, reorder or add columns: fields are matched by the header text. Values are written as plain text, never as formulas. If access is lost, submissions wait and are retried; after 10 failures in a row the channel is paused until the user fixes the sharing and turns it on again (set_channel_enabled).

## Site data from a sheet

1. The collection must be public (moderated collections take their documents from visitors). Create it first with save_collection if needed, with a schema so values get the right types: the sheet sends text, and a `number` field turns `1 290,50` into `1290.5`, a `boolean` accepts yes/no, true/false, да/нет, 1/0, an `array` splits on commas.
2. Row 1 of the sheet holds the field names, exactly as the pages read them (for example `name`, `price`, `description`). Every other non-empty row becomes a document, in the order of the rows. A column named `id` gives stable document IDs (a-z, 0-9, - and _); without it IDs are `row-<number>` and change when rows move.
3. Tell the user that the sheet becomes the source of truth: linking replaces the collection's current documents, and later edits made in the chat are overwritten by the next synchronization. After they agree, call link_sheet with the link and `confirm` set to the collection name. Choose `interval_minutes` (5 to 1440) when the site should follow the sheet by itself, for example 15; 0 means only on request.
4. The pages keep reading `/_dropsite/data/<name>` as described in topic `data`: nothing changes in the site's code.
5. When the user changed the sheet and wants the site updated now, call sync_sheet. get_sheet_link shows the state.

If some rows are wrong (a text where the schema wants a number, a missing required field, a repeated id), nothing changes on the site and the result lists the problems by row number and column: tell the user which cells to fix in the sheet, then synchronize again. unlink_sheet stops the synchronization and keeps the data.

There is no two-way synchronization: changes made on the site are not written back to the sheet.
