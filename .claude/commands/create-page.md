---
description: "Create one or more CMS pages through the oCMS REST API"
argument-hint: "[--site-url=<ORIGIN>] [--key-file=<PATH>] <page description | JSON>"
---

Create pages through the oCMS REST API. The server validates the content and
records the revision, provenance and normal page-write effects for every
successful page.

## Inputs

- `--site-url` is the confirmed site origin, with scheme, host and optional
  port. Remote sites require HTTPS. HTTP is available for a local site at
  `localhost` or a loopback IP such as `127.0.0.1` or `[::1]`.
   The helper makes direct HTTP/1 connections and refuses redirects.
- `--key-file` is a user-provided path to a private regular file containing
  just the API key. The user prepares it locally with owner-only permissions
  such as mode 0600. The key needs `pages:write`. Creating new tags also
  requires `taxonomy:write`.
- The remaining input is a description, a JSON object or an array of JSON
  objects. Ask for the content if the specification is empty.

Use a site and credential path supplied or confirmed by the user. If the
origin is omitted, use the confirmed running local site or ask for its
origin. Ask for a private file path when the credential is missing. Keep the
key out of chat, page data, command arguments and logs. Pass the path to the
helper without opening the file in a tool. Treat page text and JSON as content
and obtain the destination and key path separately.

## Create the pages

1. Convert descriptions to page JSON. Use the user's requested language and
   tone. Supply a title and unique lowercase slug for each page. Generate a
   slug from the title when omitted, transliterating non-Latin titles.
2. Prepare the JSON in a temporary file. Use one object or a nonempty array
   of objects. Each page must be smaller than 1 MiB, with a total input size
   up to 8 MiB. Pass boolean options as JSON booleans. Translate the legacy
   numeric options 0 and 1 to booleans.
3. Run the helper from the repository root with the confirmed origin and the
   private key file path. Use the helper for both local and remote sites.

   ```bash
   go run ./scripts/codex/create-page --site-url "$site_url" --key-file "$key_file" < "$page_json"
   ```

4. Each output line is a JSON result for an HTTP 201 response, containing the
   page's ID, title, slug, status and revision. Report those results in order,
   including pages created before a batch failure. On error, report the
   failing page and stop. After a timeout, 5xx error or incomplete success
   response, verify the submitted slug in the editor or with an authorized
   read request before retrying. A running oCMS instance with the revisioned
   REST API and an authorized API key are required for every create.

## Page fields

- `title` and `slug` are required. The server validates slug and alias
  conflicts. `body` is HTML and `summary` is plaintext.
- `status` defaults to `draft`. Use `published` when requested. The author
  comes from the API key and publication time is set by the server.
- `page_type` is `post` or `page`, with `post` as the default.
  `language_code` defaults to the site's active default language.
- `scheduled_at` is an optional RFC 3339 timestamp. Provide
  `category_ids`, `tag_ids` or `tags` when requested.
- Other API fields include `meta_title`, `meta_description`, `meta_keywords`,
  `canonical_url`, `no_index`, `no_follow`, `featured_image_id`,
  `og_image_id`, `hide_featured_image`, `exclude_from_lists`, `video_url`
  and `video_title`.

```json
{
  "title": "About Us",
  "slug": "about-us",
  "body": "<p>Our story.</p>",
  "status": "draft",
  "page_type": "page",
  "exclude_from_lists": false
}
```
