// Package designref turns a reference web page and its stylesheets into a
// compact design brief: palette, typography, spacing, layout outline and
// notable components. The brief never carries the page's content: no texts
// except the title, no image addresses, no class names, no scripts. It also
// builds a sanitized copy of the page for the screenshot renderer.
package designref

// Limits of the brief.
const (
	MaxPalette      = 8
	MaxCustomProps  = 40
	MaxPropName     = 40
	MaxPropValue    = 80
	MaxFonts        = 6
	MaxFontSizes    = 8
	MaxSections     = 30
	MaxTitle        = 120
	maxCommonValues = 6
)

// Palette roles.
const (
	RoleBackground = "background"
	RoleSurface    = "surface"
	RoleText       = "text"
	RoleMuted      = "muted"
	RoleAccent     = "accent"
	RoleAccent2    = "accent2"
	RoleBorder     = "border"
	RoleOther      = "other"
)

// Color schemes.
const (
	SchemeLight = "light"
	SchemeDark  = "dark"
)

// Screenshot statuses.
const (
	ShotIncluded    = "included"
	ShotUnavailable = "unavailable"
)

// Brief is the design brief of a reference page.
type Brief struct {
	URL              string       `json:"url" jsonschema:"the page that was analyzed, after redirects"`
	Title            string       `json:"title,omitempty" jsonschema:"the page title; the only text taken from the page"`
	ColorScheme      string       `json:"color_scheme" jsonschema:"light or dark"`
	HasDarkMode      bool         `json:"has_dark_mode" jsonschema:"the page has its own dark theme"`
	Palette          []Color      `json:"palette" jsonschema:"dominant colors with a guessed role, most used first"`
	CustomProperties []CustomProp `json:"custom_properties" jsonschema:"CSS custom properties with colors, sizes or fonts"`
	Fonts            []Font       `json:"fonts" jsonschema:"font families with their role"`
	FontSizes        []string     `json:"font_sizes" jsonschema:"font size scale, largest first"`
	Spacing          Tendency     `json:"spacing" jsonschema:"padding, margin and gap tendency; tendency is compact, regular or airy"`
	Radius           Tendency     `json:"radius" jsonschema:"corner radius tendency; tendency is sharp, subtle, rounded or pill"`
	Shadows          string       `json:"shadows" jsonschema:"none, subtle or strong"`
	Outline          []Section    `json:"outline" jsonschema:"page sections in order, without their texts"`
	Components       []string     `json:"components" jsonschema:"notable components, e.g. sticky_header, hero_image, cards, gallery"`
	Stylesheets      Stylesheets  `json:"stylesheets"`
	Summary          string       `json:"summary" jsonschema:"short description of the style for the assistant"`
}

// Color is one palette entry.
type Color struct {
	Hex  string `json:"hex" jsonschema:"#rrggbb"`
	Role string `json:"role" jsonschema:"background, surface, text, muted, accent, accent2, border or other"`
	Uses int    `json:"uses"`
}

// CustomProp is a CSS custom property declared by the page.
type CustomProp struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Font is a font family the page uses.
type Font struct {
	Family string `json:"family"`
	Role   string `json:"role" jsonschema:"headings, body or other"`
	Web    bool   `json:"web" jsonschema:"a web font the page loads (not a system font)"`
}

// Tendency is a summary of a group of length values.
type Tendency struct {
	Values    []string `json:"values" jsonschema:"most common values"`
	Tendency  string   `json:"tendency"`
	MedianPx  float64  `json:"median_px,omitempty"`
	CountUses int      `json:"uses"`
}

// Section is one block of the page outline.
type Section struct {
	Kind     string   `json:"kind" jsonschema:"header, nav, hero, section, cards, gallery, form, footer, ..."`
	Headings []string `json:"headings,omitempty" jsonschema:"heading levels inside, in order, e.g. h2, h3"`
	Images   int      `json:"images,omitempty"`
	Repeated int      `json:"repeated_items,omitempty" jsonschema:"number of similar items, e.g. cards in a grid"`
}

// Stylesheets reports which stylesheets were read.
type Stylesheets struct {
	Fetched   int  `json:"fetched" jsonschema:"same-site stylesheets that were read"`
	Inline    int  `json:"inline" jsonschema:"style blocks inside the page"`
	Skipped   int  `json:"skipped" jsonschema:"stylesheets that were not read: other servers, limits or errors"`
	Truncated bool `json:"truncated,omitempty" jsonschema:"limits cut the analysis short"`
}
