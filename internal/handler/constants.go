package handler

// Redirect URL constants for admin handlers.
// These are used with http.Redirect() after form submissions.
const (
	// Base admin routes
	redirectAdmin           = "/admin"
	redirectAdminPages      = "/admin/pages"
	redirectAdminPagesNew   = "/admin/pages/new"
	redirectAdminMedia      = "/admin/media"
	redirectAdminMediaUpload = "/admin/media/upload"
	redirectAdminWebhooks   = "/admin/webhooks"
	redirectAdminWebhooksNew = "/admin/webhooks/new"
	redirectAdminUsers      = "/admin/users"
	redirectAdminUsersNew   = "/admin/users/new"
	redirectAdminTags       = "/admin/tags"
	redirectAdminTagsNew    = "/admin/tags/new"
	redirectAdminCategories = "/admin/categories"
	redirectAdminCategoriesNew = "/admin/categories/new"
	redirectAdminLanguages  = "/admin/languages"
	redirectAdminLanguagesNew = "/admin/languages/new"
	redirectAdminThemes     = "/admin/themes"
	redirectAdminThemesSlash = "/admin/themes/"
	redirectAdminMenus      = "/admin/menus"
	redirectAdminMenusNew   = "/admin/menus/new"
	redirectAdminAPIKeys    = "/admin/api-keys"
	redirectAdminAPIKeysNew = "/admin/api-keys/new"
	redirectAdminAPIKeysSlash = "/admin/api-keys/"
	redirectAdminForms      = "/admin/forms"
	redirectAdminFormsNew   = "/admin/forms/new"
	redirectAdminCache      = "/admin/cache"
	redirectAdminConfig     = "/admin/config"
	redirectAdminEvents     = "/admin/events"

	// Authentication routes
	redirectLogin = "/login"

	// Frontend routes
	redirectRoot     = "/"
	redirectTag      = "/tag/"
	redirectCategory = "/category/"

	// Theme settings
	pathSettings = "/settings"

	// Format strings for ID-based routes (use with fmt.Sprintf)
	redirectAdminPagesID          = "/admin/pages/%d"
	redirectAdminPagesIDVersions  = "/admin/pages/%d/versions"
	redirectAdminMediaID          = "/admin/media/%d"
	redirectAdminWebhooksID       = "/admin/webhooks/%d"
	redirectAdminWebhooksIDDeliveries = "/admin/webhooks/%d/deliveries"
	redirectAdminUsersID          = "/admin/users/%d"
	redirectAdminTagsID           = "/admin/tags/%d"
	redirectAdminCategoriesID     = "/admin/categories/%d"
	redirectAdminLanguagesID      = "/admin/languages/%d"
	redirectAdminMenusID          = "/admin/menus/%d"
	redirectAdminFormsID          = "/admin/forms/%d"
	redirectAdminFormsIDSubmissions = "/admin/forms/%d/submissions"
)
