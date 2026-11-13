# Shared workspaces: sites of a team

A workspace is shared by a team (a company, an agency, two founders). Every member can do everything with every site of the workspace that an owner can: publish, change files, read requests, edit data, set up domains and forms, schedule, delete. There are no roles and no read-only access. A site is either personal (only the user) or belongs to one workspace; list_sites shows the workspace of each site.

## Typical requests

- "Let Anna work on our sites too": list_workspaces. If there is no workspace yet, create_workspace with the team or company name. Then add_workspace_member with Anna's e-mail and move the sites there (see below).
- "Make a new site for the team": create_site, clone_site or create_site_from_template with `workspace` set to the workspace_id. A site published with deploy_files or begin_upload without a site is personal: move it afterwards.
- "Who has access?": list_workspaces shows the members.

## Rules

- Only people who already have an account on this Dropsite server can be added. You cannot invite new people or create accounts: if add_workspace_member answers `workspace_user_not_found`, tell the user to ask the Dropsite administrator to create the account first.
- Moving a site changes who can edit it. Before move_site, say which site (name and link) goes where and who will be able to change it, wait for an explicit "yes", then pass the site's slug or ID in `confirm`. Links, versions, forms, data and settings stay the same; visitors notice nothing. Only the person who created the site can move it (`workspace_move_forbidden` otherwise).
- Removing a member (remove_workspace_member) takes away access to every site of the workspace, even sites that person created. Ask first and pass the same e-mail in `confirm`. The user's own e-mail means leaving the workspace. The last member cannot leave while the workspace has sites (`workspace_last_member`): move the sites to personal or delete them first; an empty workspace disappears with its last member.
- Limits: a site in a workspace counts against the site limit and size limit of the person who created it, not of the person publishing.
- Renaming or deleting a workspace is done in the web interface (Workspaces).
