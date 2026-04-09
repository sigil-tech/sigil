package sources

import "strings"

// domainCategories maps exact domains to activity categories.
var domainCategories = map[string]string{
	"github.com":               "development",
	"gitlab.com":               "development",
	"bitbucket.org":            "development",
	"stackoverflow.com":        "development",
	"stackexchange.com":        "development",
	"codepen.io":               "development",
	"replit.com":               "development",
	"npmjs.com":                "development",
	"pypi.org":                 "development",
	"pkg.go.dev":               "development",
	"crates.io":                "development",
	"hub.docker.com":           "development",
	"vercel.com":               "development",
	"netlify.com":              "development",
	"render.com":               "development",
	"railway.app":              "development",
	"grafana.com":              "development",
	"datadoghq.com":            "development",
	"sentry.io":                "development",
	"console.aws.amazon.com":   "development",
	"console.cloud.google.com": "development",
	"portal.azure.com":         "development",

	"developer.mozilla.org": "documentation",
	"react.dev":             "documentation",
	"docs.python.org":       "documentation",
	"doc.rust-lang.org":     "documentation",
	"docs.rs":               "documentation",
	"typescriptlang.org":    "documentation",
	"man7.org":              "documentation",

	"mail.google.com":     "communication",
	"outlook.live.com":    "communication",
	"outlook.office.com":  "communication",
	"slack.com":           "communication",
	"teams.microsoft.com": "communication",
	"discord.com":         "communication",

	"notion.so":                "project_management",
	"linear.app":               "project_management",
	"jira.atlassian.com":       "project_management",
	"confluence.atlassian.com": "project_management",
	"asana.com":                "project_management",
	"monday.com":               "project_management",
	"trello.com":               "project_management",
	"clickup.com":              "project_management",
	"figma.com":                "project_management",
	"miro.com":                 "project_management",

	"scholar.google.com": "research",
	"arxiv.org":          "research",
	"wikipedia.org":      "research",

	"twitter.com":          "social",
	"x.com":                "social",
	"linkedin.com":         "social",
	"reddit.com":           "social",
	"news.ycombinator.com": "social",

	"youtube.com": "entertainment",
	"netflix.com": "entertainment",
	"twitch.tv":   "entertainment",
	"spotify.com": "entertainment",

	"docs.google.com":     "documentation",
	"drive.google.com":    "documentation",
	"calendar.google.com": "communication",
	"meet.google.com":     "meeting",
	"maps.google.com":     "other",

	"stripe.com":     "other",
	"shopify.com":    "other",
	"hubspot.com":    "other",
	"salesforce.com": "other",
	"canva.com":      "other",
}

// domainPrefixCategories maps URL prefixes to categories.
var domainPrefixCategories = map[string]string{
	"docs.":      "documentation",
	"developer.": "documentation",
	"api.":       "documentation",
	"wiki.":      "documentation",
	"learn.":     "documentation",
	"mail.":      "communication",
	"meet.":      "meeting",
}

// ClassifyDomain maps a domain to an activity category.
// Returns "other" if no match.
func ClassifyDomain(domain string) string {
	if cat, ok := domainCategories[domain]; ok {
		return cat
	}

	// Check prefixes (e.g. "docs.example.com" → "documentation").
	for prefix, cat := range domainPrefixCategories {
		if strings.HasPrefix(domain, prefix) {
			return cat
		}
	}

	return "other"
}
