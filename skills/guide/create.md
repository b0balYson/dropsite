# Create a site and publish it

1. Understand the goal in one or two questions at most: who the site is for, the name, contacts, what visitors should do (call, order, leave a request). If the user gave enough, start right away and use sensible placeholders they can change later.
2. Build a static site: `index.html` at the root, other pages as `about.html` or `about/index.html`, styles and scripts in separate files or inline. Follow the `assets` topic: every image, font and script ships with the site, sizes are small, the page works on a phone first.
3. Publish:
   - Up to 1 MB in total: call deploy_files with every file and no `site`; it creates a new site and returns its link. Pass `title` with a human name of the site.
   - Bigger sites (several photos): use an upload session, see `large_sites`.
   - To bring in photos or fonts from the web, use the `fetch` list of update_site_files or upload_files (the server downloads them; https only, public addresses only). Prefer real photos the user gave or freely licensed ones; never hotlink them.
   - A desktop client with deploy_folder publishes a folder from the computer: pass only the folder with `index.html` (the build output), never a project or home folder.
4. If the user wants a readable address, set it right away (update_site or set_slug, for example `coffee-river`), see `addresses`.
5. Reply in plain words: "Your site is live: <link>". Mention one or two next steps in one line: own domain, a form for requests, a password while it is a draft.

Good defaults for a small business site: a hero with the name and one clear action (call, map, order), menu or services with prices, photos, opening hours, address with a map link (a plain link to a map service, not an embedded heavy widget), contacts, a request form (see `forms`), `404.html`, a favicon, `<title>` and `<meta name="description">`, Open Graph tags, `lang` on `<html>`.

Serving rules worth knowing: `/` and `/dir/` serve `index.html`; `/about` serves `about.html`; `404.html` at the root is the not-found page; `200.html` at the root makes a single-page app answer every unknown path; files and folders whose names start with a dot are not served (except `/.well-known/`); `/_form/` and `/_dropsite/` belong to the platform. HTML is not cached by browsers, other files for one hour: when you change an image, give it a new file name or the user may see the old one for an hour.
