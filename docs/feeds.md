# RSS and Atom feeds

Feeds are built into core and require no module activation or additional
configuration beyond a valid `site_url` (or `OCMS_SITE_URL` at startup).

| Subscription | RSS 2.0 | Atom 1.0 |
|---|---|---|
| All posts | `/rss.xml` | `/atom.xml` |
| Category | `/category/{slug}/rss.xml` | `/category/{slug}/atom.xml` |
| Tag | `/tag/{slug}/rss.xml` | `/tag/{slug}/atom.xml` |

Unprefixed URLs always use the active default language. Other active languages
use their URL prefix, for example `/ru/rss.xml` or
`/ru/category/technology/rss.xml`. Cookies, `Accept-Language` and `?lang=` do
not change a feed URL's language. A taxonomy slug must belong to that language;
missing terms and unknown or inactive language prefixes return 404.

## Included content

Each feed contains at most 20 published posts, newest publication first, with
the page ID as the descending tie-breaker. If a published post has no publication
timestamp, its creation timestamp supplies both the displayed date and ordering.
Ordering compares actual instants across time zones and retains fractional seconds.
Language, taxonomy and valid article-slug filters apply before the limit, so
unroutable legacy posts do not consume subscription slots. Static pages, drafts,
scheduled-but-unpublished posts and posts marked **Exclude from lists** are
omitted. **No index** controls search
engine indexing and does not remove a public post from feeds.

Entries contain the title, absolute article link, public author name, publication
and modification dates, and a plain-text summary. The saved summary takes
precedence; otherwise the body becomes an excerpt of at most 300 Unicode
characters, including the ellipsis. Markup and script/style contents are removed.
RSS escapes literal markup for display as text; Atom summaries use `type="text"`.
Full article HTML and media enclosures are not included.

Entry identifiers use the configured site origin and page ID (`/page/{id}`),
independent of the article slug. Renaming or updating a post retains its identity;
changing the configured origin changes the identifier. Author email addresses
are never emitted. Posts without a publication timestamp use their creation
timestamp. Empty feeds remain valid documents with site/language/taxonomy
metadata supplying the feed update date.

## HTTP behavior

GET returns UTF-8 XML with `application/rss+xml` or `application/atom+xml`.
HEAD returns the same representation headers without a body, including when
gzip or deflate compression is negotiated. Feeds are generated from current
database data on every request, with `Cache-Control: public, no-cache` and a
deterministic weak ETag that remains valid under compression. A matching
`If-None-Match` returns 304 without a body; publishing, editing, unpublishing,
deletion and taxonomy association changes are reflected on the next request.

Absolute URLs come from `site_url`, never request or proxy headers. An absent
or invalid HTTP(S) origin returns 503. Database or serialization failures return
500 rather than an apparently empty feed. Error responses use `no-store`.
Internal analytics already excludes `.xml` requests, so reader polling does
not count as page views.

## Theme discovery

The shipped themes, starter and fallback layout emit subscription discovery
links in the HTML head. Every normal frontend page has the current language's
site subscriptions; category and tag archives add their contextual subscriptions.
No feed links are advertised when the site origin is unconfigured or invalid.

Custom HTML themes can render the additive `BaseTemplateData.FeedLinks` list:

```html
{{range .FeedLinks}}
<link rel="alternate" type="{{.MIMEType}}" title="{{.Title}}" href="{{.URL}}">
{{end}}
```

Each `FeedLink` exposes `Title`, `MIMEType` and absolute `URL`. Existing themes
continue working without this addition. Feed paths are reserved by the Migrator
alongside the other core public routes.

The serializers follow the [RSS 2.0 specification](https://www.rssboard.org/rss-specification)
and [Atom 1.0 standard](https://www.rfc-editor.org/rfc/rfc4287). RSS includes Atom
self/update and Dublin Core creator extensions for discovery, modification dates
and author display names.
