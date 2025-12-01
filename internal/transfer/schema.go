// Package transfer provides import/export functionality for oCMS content.
package transfer

import "time"

// ExportVersion is the current version of the export format.
const ExportVersion = "1.0"

// ExportData represents the complete export structure.
type ExportData struct {
	Version    string            `json:"version"`
	ExportedAt time.Time         `json:"exported_at"`
	Site       ExportSite        `json:"site"`
	Languages  []ExportLanguage  `json:"languages,omitempty"`
	Users      []ExportUser      `json:"users,omitempty"`
	Pages      []ExportPage      `json:"pages,omitempty"`
	Categories []ExportCategory  `json:"categories,omitempty"`
	Tags       []ExportTag       `json:"tags,omitempty"`
	Media      []ExportMedia     `json:"media,omitempty"`
	Menus      []ExportMenu      `json:"menus,omitempty"`
	Forms      []ExportForm      `json:"forms,omitempty"`
	Config     map[string]string `json:"config,omitempty"`
}

// ExportSite contains basic site information.
type ExportSite struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// ExportLanguage represents a language configuration.
type ExportLanguage struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NativeName string `json:"native_name"`
	IsDefault  bool   `json:"is_default"`
	IsActive   bool   `json:"is_active"`
	Direction  string `json:"direction"`
	Position   int64  `json:"position"`
}

// ExportUser represents a user for export (no passwords).
type ExportUser struct {
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// ExportPage represents a page with all its relationships.
type ExportPage struct {
	ID            int64            `json:"id"`
	Title         string           `json:"title"`
	Slug          string           `json:"slug"`
	Body          string           `json:"body"`
	Status        string           `json:"status"`
	AuthorEmail   string           `json:"author_email"`
	Categories    []string         `json:"categories,omitempty"`
	Tags          []string         `json:"tags,omitempty"`
	SEO           *ExportPageSEO   `json:"seo,omitempty"`
	FeaturedImage *ExportMediaRef  `json:"featured_image,omitempty"`
	LanguageCode  string           `json:"language_code,omitempty"`
	Translations  map[string]int64 `json:"translations,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	PublishedAt   *time.Time       `json:"published_at,omitempty"`
	ScheduledAt   *time.Time       `json:"scheduled_at,omitempty"`
}

// ExportPageSEO contains SEO metadata for a page.
type ExportPageSEO struct {
	MetaTitle       string          `json:"meta_title,omitempty"`
	MetaDescription string          `json:"meta_description,omitempty"`
	MetaKeywords    string          `json:"meta_keywords,omitempty"`
	OgImage         *ExportMediaRef `json:"og_image,omitempty"`
	NoIndex         bool            `json:"no_index,omitempty"`
	NoFollow        bool            `json:"no_follow,omitempty"`
	CanonicalURL    string          `json:"canonical_url,omitempty"`
}

// ExportMediaRef is a reference to a media item by UUID.
type ExportMediaRef struct {
	UUID     string `json:"uuid"`
	Filename string `json:"filename"`
}

// ExportCategory represents a category with hierarchy support.
type ExportCategory struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	Slug         string           `json:"slug"`
	Description  string           `json:"description,omitempty"`
	ParentSlug   string           `json:"parent_slug,omitempty"`
	Position     int64            `json:"position"`
	LanguageCode string           `json:"language_code,omitempty"`
	Translations map[string]int64 `json:"translations,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// ExportTag represents a tag.
type ExportTag struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	Slug         string           `json:"slug"`
	LanguageCode string           `json:"language_code,omitempty"`
	Translations map[string]int64 `json:"translations,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// ExportMedia represents media metadata (not the actual file).
type ExportMedia struct {
	UUID       string          `json:"uuid"`
	Filename   string          `json:"filename"`
	MimeType   string          `json:"mime_type"`
	Size       int64           `json:"size"`
	Width      *int64          `json:"width,omitempty"`
	Height     *int64          `json:"height,omitempty"`
	Alt        string          `json:"alt,omitempty"`
	Caption    string          `json:"caption,omitempty"`
	FolderPath string          `json:"folder_path,omitempty"`
	UploadedBy string          `json:"uploaded_by"`
	Variants   []ExportVariant `json:"variants,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ExportVariant represents a media variant (thumbnail, etc.).
type ExportVariant struct {
	Type   string `json:"type"`
	Width  int64  `json:"width"`
	Height int64  `json:"height"`
	Size   int64  `json:"size"`
}

// ExportMenu represents a navigation menu.
type ExportMenu struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	Slug         string           `json:"slug"`
	LanguageCode string           `json:"language_code,omitempty"`
	Items        []ExportMenuItem `json:"items,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// ExportMenuItem represents a menu item.
type ExportMenuItem struct {
	ID       int64            `json:"id"`
	Title    string           `json:"title"`
	URL      string           `json:"url,omitempty"`
	Target   string           `json:"target,omitempty"`
	PageSlug string           `json:"page_slug,omitempty"`
	CssClass string           `json:"css_class,omitempty"`
	IsActive bool             `json:"is_active"`
	Position int64            `json:"position"`
	Children []ExportMenuItem `json:"children,omitempty"`
}

// ExportForm represents a form definition.
type ExportForm struct {
	ID             int64                  `json:"id"`
	Name           string                 `json:"name"`
	Slug           string                 `json:"slug"`
	Title          string                 `json:"title"`
	Description    string                 `json:"description,omitempty"`
	SuccessMessage string                 `json:"success_message,omitempty"`
	EmailTo        string                 `json:"email_to,omitempty"`
	IsActive       bool                   `json:"is_active"`
	Fields         []ExportFormField      `json:"fields,omitempty"`
	Submissions    []ExportFormSubmission `json:"submissions,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// ExportFormField represents a form field.
type ExportFormField struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	HelpText    string `json:"help_text,omitempty"`
	Options     string `json:"options,omitempty"`
	Validation  string `json:"validation,omitempty"`
	IsRequired  bool   `json:"is_required"`
	Position    int64  `json:"position"`
}

// ExportFormSubmission represents a form submission.
type ExportFormSubmission struct {
	Data      string    `json:"data"`
	IPAddress string    `json:"ip_address,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

// ExportOptions configures what to include in the export.
type ExportOptions struct {
	IncludeUsers       bool   `json:"include_users"`
	IncludePages       bool   `json:"include_pages"`
	IncludeCategories  bool   `json:"include_categories"`
	IncludeTags        bool   `json:"include_tags"`
	IncludeMedia       bool   `json:"include_media"`
	IncludeMenus       bool   `json:"include_menus"`
	IncludeForms       bool   `json:"include_forms"`
	IncludeSubmissions bool   `json:"include_submissions"`
	IncludeConfig      bool   `json:"include_config"`
	IncludeLanguages   bool   `json:"include_languages"`
	PageStatus         string `json:"page_status"` // "all", "published", "draft"
}

// DefaultExportOptions returns options that include everything.
func DefaultExportOptions() ExportOptions {
	return ExportOptions{
		IncludeUsers:       true,
		IncludePages:       true,
		IncludeCategories:  true,
		IncludeTags:        true,
		IncludeMedia:       true,
		IncludeMenus:       true,
		IncludeForms:       true,
		IncludeSubmissions: false, // submissions excluded by default for privacy
		IncludeConfig:      true,
		IncludeLanguages:   true,
		PageStatus:         "all",
	}
}
