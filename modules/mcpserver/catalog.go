// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// toolSpec describes one MCP tool. toolCatalog is the single source of truth
// for the tool set: server registration, the admin page and the drift tests
// all read it, so no tool exists without a description and a declared
// relationship to the REST v2 API.
type toolSpec struct {
	Name        string
	Title       string
	Description string
	// RESTOperations are the REST v2 operationIds whose data this tool
	// returns, under the same visibility rules.
	RESTOperations []string
	// MCPOnlyReason explains a tool that has no REST counterpart.
	MCPOnlyReason string
	register      func(m *Module, srv *mcp.Server, spec toolSpec)
}

// toolCatalog returns every tool the server registers, in display order.
func toolCatalog() []toolSpec {
	return []toolSpec{
		{
			Name:  "get_site_info",
			Title: "Get site info",
			Description: "Returns the site's name, description and public URL, its active languages, " +
				"and what the calling API key may access (permissions, whether unpublished drafts are visible). " +
				"Call this first.",
			RESTOperations: []string{"auth"},
			register:       registerTool((*Module).getSiteInfo),
		},
		{
			Name:  "search_pages",
			Title: "Search pages",
			Description: "Searches page titles and bodies for the given words and returns matching pages " +
				"with a short plain-text excerpt. Only published pages are searched unless drafts are visible " +
				"to this key (see get_site_info). Use get_page to read a result in full.",
			MCPOnlyReason: "REST v2 has no search endpoint; this reuses the site's full-text search",
			register:      registerTool((*Module).searchPages),
		},
		{
			Name:  "list_pages",
			Title: "List pages",
			Description: "Lists pages, newest first, optionally filtered by status, category_id or tag_id. " +
				"Returns summaries without bodies; use get_page for the content. " +
				"Drafts are listed only when visible to this key.",
			RESTOperations: []string{"listPages"},
			register:       registerTool((*Module).listPages),
		},
		{
			Name:  "get_page",
			Title: "Get page",
			Description: "Returns one page by id or by slug, with its body, SEO metadata, author name, " +
				"categories and tags. body_format \"html\" (default) returns the stored HTML; \"markdown\" " +
				"returns a compact Markdown conversion that is easier to read but drops embeds and styling.",
			RESTOperations: []string{"getPage", "getPageBySlug"},
			register:       registerTool((*Module).getPage),
		},
		{
			Name:  "list_media",
			Title: "List media",
			Description: "Lists media library items (images, documents, videos), newest first, optionally " +
				"filtered by type, folder_id or a search of filenames and alt text. URLs are site-relative " +
				"paths; prefix them with the site URL from get_site_info.",
			RESTOperations: []string{"listMedia"},
			register:       registerTool((*Module).listMedia),
		},
		{
			Name:  "get_media",
			Title: "Get media",
			Description: "Returns one media item by id, including its generated image sizes, its folder " +
				"and per-language alt text and captions.",
			RESTOperations: []string{"getMedia"},
			register:       registerTool((*Module).getMedia),
		},
		{
			Name:           "list_tags",
			Title:          "List tags",
			Description:    "Lists tags with the number of pages using each.",
			RESTOperations: []string{"listTags"},
			register:       registerTool((*Module).listTags),
		},
		{
			Name:           "get_tag",
			Title:          "Get tag",
			Description:    "Returns one tag by id with the number of pages using it.",
			RESTOperations: []string{"getTag"},
			register:       registerTool((*Module).getTag),
		},
		{
			Name:  "list_categories",
			Title: "List categories",
			Description: "Lists categories as a nested tree (default) or as a flat list (flat=true), " +
				"each with the number of pages in it.",
			RESTOperations: []string{"listCategories"},
			register:       registerTool((*Module).listCategories),
		},
		{
			Name:           "get_category",
			Title:          "Get category",
			Description:    "Returns one category by id with its direct child categories.",
			RESTOperations: []string{"getCategory"},
			register:       registerTool((*Module).getCategory),
		},
	}
}

// deferredToWritePhase explains why the REST write operations have no tool.
const deferredToWritePhase = "write tools are not part of the read-only first release"

// restOperationsNotExposed lists REST v2 operations deliberately absent from
// the MCP tool set, with the reason. TestEveryRESTOperationHasMCPDecision
// fails when REST gains an operation that no tool mirrors and this map does
// not explain, so new REST surface always gets an explicit MCP decision.
var restOperationsNotExposed = map[string]string{
	"status":           "REST liveness probe; MCP clients use server/discover",
	"createPage":       deferredToWritePhase,
	"updatePage":       deferredToWritePhase,
	"deletePage":       deferredToWritePhase,
	"uploadMedia":      deferredToWritePhase,
	"uploadMediaBatch": deferredToWritePhase,
	"updateMedia":      deferredToWritePhase,
	"deleteMedia":      deferredToWritePhase,
	"createTag":        deferredToWritePhase,
	"updateTag":        deferredToWritePhase,
	"deleteTag":        deferredToWritePhase,
	"createCategory":   deferredToWritePhase,
	"updateCategory":   deferredToWritePhase,
	"deleteCategory":   deferredToWritePhase,
}
