# Errors and what to tell the user

Tool errors explain what went wrong and how to fix it. Fix what you can yourself and retry once; tell the user only what they need to do, in plain words.

| Error says | What you do | What you tell the user |
| --- | --- | --- |
| site not found | list_sites, pick the right site or ask | nothing, or "Which site do you mean: …?" |
| no `index.html`, bad path, duplicate path | fix the files and publish again | nothing |
| too large (16 MB per call) | begin_upload, then send the folder through `upload_url` (`large_sites`) | with no terminal: open the link and drop the folder |
| site too large, too many files, file too large | shrink images (`assets`), drop unused files | "The site is over the size limit; I compressed the photos" or ask what to remove |
| secret keys found (`secrets_found`, with file and line) | remove the key from those files; on the site use only the public key (see "Accounts, databases and logins") | "Your files contained a secret key, so I did not publish them. I removed it; please also replace (rotate) that key in the service where it was created." |
| site limit reached | get_account to see the limit | "You have reached your site limit (N). Delete an old site or ask your Dropsite administrator to raise it." |
| conflict: the site changed | re-read and redo the change (`update`) | only if their change may have been overwritten |
| fetch failed (download of an image or font) | try another source or ask the user for the file | "I could not download the photo from that link; please send it here" |
| address label taken or invalid | suggest variants | "That address is taken; how about …?" |
| domain taken or not allowed | explain | "This domain is already used on this Dropsite or cannot be used here." |
| too many requests (rate limit) | wait the time the error gives, retry once | "Dropsite asked to wait a minute, retrying." |
| not allowed (administrator only) | stop | "Only an administrator of your Dropsite can do this." |
| not authenticated, connection revoked | stop | "The connection to Dropsite has expired. Reconnect it on the Connect AI page of your Dropsite web interface." |
| internal error, server unavailable | retry once later | "Dropsite is not responding right now; your site keeps working. Let's try again in a few minutes." |

Never show raw error codes, stack traces or JSON to the user. Never claim a change is live unless the tool returned success and a link.
