package handler

// Route pattern constants for chi router registration.
const (
	// RouteRoot is the root path.
	RouteRoot = "/"
	// RouteSuffixNew is the suffix for "new" routes.
	RouteSuffixNew = "/new"
	// RouteSuffixSearch is the suffix for search routes.
	RouteSuffixSearch = "/search"
	// RouteSuffixUpload is the suffix for upload routes.
	RouteSuffixUpload = "/upload"
	// RouteSuffixReorder is the suffix for reorder routes.
	RouteSuffixReorder = "/reorder"
	// RouteSuffixMove is the suffix for move routes.
	RouteSuffixMove = "/move"
	// RouteSuffixTranslate is the suffix for translation routes.
	RouteSuffixTranslate = "/translate/{langCode}"
	// RouteSuffixFolders is the suffix for folder routes.
	RouteSuffixFolders = "/folders"

	// RouteParamID is the ID parameter pattern.
	RouteParamID = "/{id}"
	// RouteParamSlug is the slug parameter pattern.
	RouteParamSlug = "/{slug}"
	// RouteTagSlug is the tag slug route pattern.
	RouteTagSlug = "/tag/{slug}"
	// RouteCategorySlug is the category slug route pattern.
	RouteCategorySlug = "/category/{slug}"
	// RouteFormsSlug is the forms slug route pattern.
	RouteFormsSlug = "/forms/{slug}"
	// RouteSubmissionsSubID is the submissions sub-ID route pattern.
	RouteSubmissionsSubID = "/submissions/{subId}"
	// RouteItemsItemID is the items item-ID route pattern.
	RouteItemsItemID = "/items/{itemId}"
	// RouteFieldsFieldID is the fields field-ID route pattern.
	RouteFieldsFieldID = "/fields/{fieldId}"

	// RouteLogin is the login route.
	RouteLogin = "/login"
	// RouteLogout is the logout route.
	RouteLogout = "/logout"
	// RouteBlog is the blog route.
	RouteBlog = "/blog"

	// RouteUsers is the users admin route.
	RouteUsers = "/users"
	// RouteLanguages is the languages admin route.
	RouteLanguages = "/languages"
	// RoutePages is the pages admin route.
	RoutePages = "/pages"
	// RouteTags is the tags admin route.
	RouteTags = "/tags"
	// RouteCategories is the categories admin route.
	RouteCategories = "/categories"
	// RouteMedia is the media admin route.
	RouteMedia = "/media"
	// RouteMenus is the menus admin route.
	RouteMenus = "/menus"
	// RouteForms is the forms admin route.
	RouteForms = "/forms"
	// RouteWidgets is the widgets admin route.
	RouteWidgets = "/widgets"
	// RouteAPIKeys is the API keys admin route.
	RouteAPIKeys = "/api-keys"
	// RouteWebhooks is the webhooks admin route.
	RouteWebhooks = "/webhooks"
	// RouteExport is the export admin route.
	RouteExport = "/export"
	// RouteImport is the import admin route.
	RouteImport = "/import"
	// RouteConfig is the config admin route.
	RouteConfig = "/config"

	// RouteUsersID is the users ID route pattern.
	RouteUsersID = RouteUsers + RouteParamID
	// RouteLanguagesID is the languages ID route pattern.
	RouteLanguagesID = RouteLanguages + RouteParamID
	// RoutePagesID is the pages ID route pattern.
	RoutePagesID = RoutePages + RouteParamID
	// RouteTagsID is the tags ID route pattern.
	RouteTagsID = RouteTags + RouteParamID
	// RouteCategoriesID is the categories ID route pattern.
	RouteCategoriesID = RouteCategories + RouteParamID
	// RouteMediaID is the media ID route pattern.
	RouteMediaID = RouteMedia + RouteParamID
	// RouteMediaFoldersID is the media folders ID route pattern.
	RouteMediaFoldersID = RouteMedia + RouteSuffixFolders + RouteParamID
	// RouteMenusID is the menus ID route pattern.
	RouteMenusID = RouteMenus + RouteParamID
	// RouteFormsID is the forms ID route pattern.
	RouteFormsID = RouteForms + RouteParamID
	// RouteThemeSettings is the theme settings route pattern.
	RouteThemeSettings = "/themes/{name}/settings"
	// RouteWidgetsID is the widgets ID route pattern.
	RouteWidgetsID = RouteWidgets + RouteParamID
	// RouteAPIKeysID is the API keys ID route pattern.
	RouteAPIKeysID = RouteAPIKeys + RouteParamID
	// RouteWebhooksID is the webhooks ID route pattern.
	RouteWebhooksID = RouteWebhooks + RouteParamID
)

const (
	redirectAdmin              = "/admin"
	redirectAdminPages         = redirectAdmin + RoutePages
	redirectAdminPagesNew      = redirectAdminPages + RouteSuffixNew
	redirectAdminMedia         = redirectAdmin + RouteMedia
	redirectAdminMediaUpload   = redirectAdminMedia + RouteSuffixUpload
	redirectAdminWebhooks      = redirectAdmin + RouteWebhooks
	redirectAdminWebhooksNew   = redirectAdminWebhooks + RouteSuffixNew
	redirectAdminUsers         = redirectAdmin + RouteUsers
	redirectAdminUsersNew      = redirectAdminUsers + RouteSuffixNew
	redirectAdminTags          = redirectAdmin + RouteTags
	redirectAdminTagsNew       = redirectAdminTags + RouteSuffixNew
	redirectAdminCategories    = redirectAdmin + RouteCategories
	redirectAdminCategoriesNew = redirectAdminCategories + RouteSuffixNew
	redirectAdminLanguages     = redirectAdmin + RouteLanguages
	redirectAdminLanguagesNew  = redirectAdminLanguages + RouteSuffixNew
	redirectAdminThemes        = "/admin/themes"
	redirectAdminThemesSlash   = redirectAdminThemes + RouteRoot
	redirectAdminMenus         = redirectAdmin + RouteMenus
	redirectAdminMenusNew      = redirectAdminMenus + RouteSuffixNew
	redirectAdminAPIKeys       = redirectAdmin + RouteAPIKeys
	redirectAdminAPIKeysNew    = redirectAdminAPIKeys + RouteSuffixNew
	redirectAdminAPIKeysSlash  = redirectAdminAPIKeys + RouteRoot
	redirectAdminForms         = redirectAdmin + RouteForms
	redirectAdminFormsNew      = redirectAdminForms + RouteSuffixNew
	redirectAdminCache         = "/admin/cache"
	redirectAdminConfig        = redirectAdmin + RouteConfig
	redirectAdminEvents        = "/admin/events"
	redirectLogin              = RouteLogin
	redirectTag                = "/tag/"
	redirectCategory           = "/category/"
	pathSettings               = "/settings"

	redirectAdminPagesID              = redirectAdminPages + "/%d"
	redirectAdminPagesIDVersions      = redirectAdminPagesID + "/versions"
	redirectAdminMediaID              = redirectAdminMedia + "/%d"
	redirectAdminWebhooksID           = redirectAdminWebhooks + "/%d"
	redirectAdminWebhooksIDDeliveries = redirectAdminWebhooksID + "/deliveries"
	redirectAdminUsersID              = redirectAdminUsers + "/%d"
	redirectAdminTagsID               = redirectAdminTags + "/%d"
	redirectAdminCategoriesID         = redirectAdminCategories + "/%d"
	redirectAdminLanguagesID          = redirectAdminLanguages + "/%d"
	redirectAdminMenusID              = redirectAdminMenus + "/%d"
	redirectAdminFormsID              = redirectAdminForms + "/%d"
	redirectAdminFormsIDSubmissions   = redirectAdminFormsID + "/submissions"
)

// Utility constants used by main.go.
const (
	// LogCacheManagerInit is the log message for cache manager initialization.
	LogCacheManagerInit = "cache manager initialized"
	// UploadsDirPath is the default uploads directory path.
	UploadsDirPath = "./uploads"
	// HeaderContentType is the Content-Type HTTP header name.
	HeaderContentType = "Content-Type"
)
