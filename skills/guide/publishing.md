# Publishing: what the server adds to every version

## Optimization at publish time

Every new version is optimized by the server unless the user or the administrator turned it off. The site's own files are never changed:

- JPEG and PNG images get lighter WebP copies of the same size in pixels. Browsers that support WebP get the copy, others get the original, under the same address. Do not convert images to WebP yourself and do not change `<img>` links for this.
- When the site has no `robots.txt`, `sitemap.xml` or `favicon.ico`, the server serves generated ones: a robots file that allows search engines and points to the sitemap, a sitemap of all pages, a simple round icon. A file of the site with the same name always wins, so add your own when the user wants something specific (for example a real logo as `favicon.ico`). No robots file or sitemap is generated for a site hidden from search.
- Files the server adds do not appear in list_site_files or read_site_files and do not count against size limits.

get_publish_settings shows whether this is on for a site; set_publish_settings turns it off or on from the next publication. Turn it off only when the user asks, for example "serve my files exactly as I uploaded them".

## Quality check of every version

Every publication result (deploy_files, deploy_folder, update_site_files, commit_upload, rollback) has `quality`: a score from 0 to 100, a grade A to D and up to five main problems. get_quality_report shows the full list for a version: broken internal links, missing images, styles, scripts or fonts, images without alt text, pages without a title, description, viewport or `lang`, the heaviest pages and largest files, and files loaded from other servers.

- Fix what you can right away with update_site_files, without asking: wrong links and paths, a missing `<title>`, viewport or `lang`, alt text that describes the image. Download files loaded from other servers into the site with `fetch` and link them locally.
- Ask the user before inventing content: real photos for missing images, the wording of a description.
- Tell the user about the result in one plain sentence ("I checked the site: everything works" or "I fixed two broken links"), not the score or the field names, unless they ask.
