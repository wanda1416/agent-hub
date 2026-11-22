package monitor

// Alert modes persisted in the application configuration.
const (
	ModeInApp        = "inapp"
	ModeNotification = "notification"
	ModePopup        = "popup"
)

// Model describes one tracked AI product on AIHOT.
type Model struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	PageURL string `json:"pageUrl"`
}

// Models is the fixed set of monitored products, in display order. Grok Bot
// uses a hyphenated ID so it survives config round-trips unchanged.
var Models = []Model{
	{ID: "codex", Name: "Codex", Slug: "codex", PageURL: "https://aihot.news/codex-reset"},
	{ID: "antigravity", Name: "Antigravity", Slug: "antigravity", PageURL: "https://aihot.news/antigravity-reset"},
	{ID: "claude", Name: "Claude", Slug: "claude", PageURL: "https://aihot.news/claude-reset"},
	{ID: "grok-bot", Name: "Grok Bot", Slug: "grok-bot", PageURL: "https://aihot.news/grok-bot-reset"},
}

// Post is a source post backing a reset event.
type Post struct {
	Author       string  `json:"author"`
	PublishedAt  *string `json:"publishedAt"`
	Stage        string  `json:"stage"`
	Text         string  `json:"text"`
	OriginalText string  `json:"originalText"`
	URL          string  `json:"url"`
}

// Presentation carries the display qualifiers AIHOT derives for an event.
type Presentation struct {
	Status       string  `json:"status"`
	ScopeKnown   bool    `json:"scopeKnown"`
	ScopeLabel   *string `json:"scopeLabel"`
	AudienceZh   *string `json:"audienceZh"`
	ProductsZh   *string `json:"productsZh"`
	ReportedAt   *string `json:"reportedAt"`
	TimeInferred bool    `json:"timeInferred"`
}

// Estimate is AIHOT's projected landing window for an unconfirmed event.
type Estimate struct {
	From    *string `json:"from"`
	Through *string `json:"through"`
	Basis   string  `json:"basis"`
	Label   string  `json:"label"`
	Reason  string  `json:"reason"`
}

// Schedule is the time window converted from the source post's own words.
type Schedule struct {
	Precision string  `json:"precision"`
	From      *string `json:"from"`
	Through   *string `json:"through"`
	Label     string  `json:"label"`
}

// Event is one reset or reset-card event as returned by the AIHOT API.
type Event struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Label        string        `json:"label"`
	DisplayLabel string        `json:"displayLabel"`
	Presentation *Presentation `json:"presentation"`
	Estimate     *Estimate     `json:"estimate"`
	Status       string        `json:"status"`
	Title        string        `json:"title"`
	Scope        string        `json:"scope"`
	CreatedAt    *string       `json:"createdAt"`
	UpdatedAt    *string       `json:"updatedAt"`
	ConfirmedAt  *string       `json:"confirmedAt"`
	OccurredOn   *string       `json:"occurredOn"`
	Schedule     *Schedule     `json:"schedule"`
	Posts        []Post        `json:"posts"`
	URL          string        `json:"url"`

	// Active is computed for the UI: the reset has been announced but has not
	// landed yet, so it is worth showing prominently and notifying about.
	Active bool `json:"active"`
}

// IsActive reports whether an event is an announced reset that has not
// produced a confirmation yet. The presentation status is authoritative when
// present because the legacy status field stays "announced" for events whose
// estimated window has already passed.
func (e Event) IsActive() bool {
	if e.Presentation != nil && e.Presentation.Status != "" {
		switch e.Presentation.Status {
		case "announced", "in_progress":
			return true
		default:
			return false
		}
	}
	return e.Status == "announced"
}

// SiteMonitor is the public monitoring progress reported by AIHOT.
type SiteMonitor struct {
	Status         string  `json:"status"`
	LastAttemptAt  *string `json:"lastAttemptAt"`
	LastVerifiedAt *string `json:"lastVerifiedAt"`
}

// ModelSnapshot is the per-model state rendered by the monitor page.
type ModelSnapshot struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	PageURL        string  `json:"pageUrl"`
	Followed       bool    `json:"followed"`
	Status         string  `json:"status"` // ok | error | disabled
	Error          string  `json:"error,omitempty"`
	SiteStatus     string  `json:"siteStatus,omitempty"`
	CheckedAt      string  `json:"checkedAt,omitempty"`
	LastVerifiedAt string  `json:"lastVerifiedAt,omitempty"`
	Events         []Event `json:"events"`
}

// Snapshot is the complete monitor state sent to the frontend.
type Snapshot struct {
	Enabled    bool            `json:"enabled"`
	Interval   int             `json:"interval"`
	AlertMode  string          `json:"alertMode"`
	AlertSound bool            `json:"alertSound"`
	LastPoll   string          `json:"lastPoll,omitempty"`
	Models     []ModelSnapshot `json:"models"`
}

// Alert groups the newly announced events of one model into a single alert so
// a poll can notify once per product instead of once per event.
type Alert struct {
	ModelID   string  `json:"modelId"`
	ModelName string  `json:"modelName"`
	PageURL   string  `json:"pageUrl"`
	Events    []Event `json:"events"`
}

// Config is the subset of the application configuration the watcher needs.
type Config struct {
	Enabled    bool
	Interval   int // minutes
	Models     []string
	AlertMode  string
	AlertSound bool
}

func (c Config) follows(id string) bool {
	for _, model := range c.Models {
		if model == id {
			return true
		}
	}
	return false
}
