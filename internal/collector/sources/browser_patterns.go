package sources

import "strings"

// titleDomainSuffixes maps page title suffixes to domains.
// Sorted longest-first within each group for correct matching.
var titleDomainSuffixes = []struct {
	suffix string
	domain string
}{
	// Development
	{" · GitHub", "github.com"},
	{" · Pull Request", "github.com"},
	{" · GitLab", "gitlab.com"},
	{" / Bitbucket", "bitbucket.org"},
	{" - Stack Overflow", "stackoverflow.com"},
	{" - Stack Exchange", "stackexchange.com"},
	{" - CodePen", "codepen.io"},
	{" - JSFiddle", "jsfiddle.net"},
	{" - Replit", "replit.com"},
	{" - npm", "npmjs.com"},
	{" - PyPI", "pypi.org"},
	{" — pkg.go.dev", "pkg.go.dev"},
	{" — crates.io", "crates.io"},
	{" - Docker Hub", "hub.docker.com"},
	{" - Vercel", "vercel.com"},
	{" - Netlify", "netlify.com"},
	{" - Render", "render.com"},
	{" - Railway", "railway.app"},
	{" | Grafana", "grafana.com"},
	{" | Datadog", "datadoghq.com"},
	{" - Sentry", "sentry.io"},

	// Documentation
	{" — React", "react.dev"},
	{" | MDN", "developer.mozilla.org"},
	{" - MDN Web Docs", "developer.mozilla.org"},
	{" – docs.rs", "docs.rs"},
	{" — Rust", "doc.rust-lang.org"},
	{" — Python", "docs.python.org"},
	{" | TypeScript", "typescriptlang.org"},

	// Communication
	{" - Gmail", "mail.google.com"},
	{" - Inbox", "mail.google.com"},
	{" - Outlook", "outlook.live.com"},
	{" | Slack", "slack.com"},
	{" - Slack", "slack.com"},
	{" | Microsoft Teams", "teams.microsoft.com"},
	{" | Discord", "discord.com"},
	{" - Discord", "discord.com"},

	// Project Management
	{" | Notion", "notion.so"},
	{" - Notion", "notion.so"},
	{" - Linear", "linear.app"},
	{" - Jira", "jira.atlassian.com"},
	{" | Atlassian", "atlassian.com"},
	{" - Asana", "asana.com"},
	{" | Monday.com", "monday.com"},
	{" | Trello", "trello.com"},
	{" - ClickUp", "clickup.com"},
	{" | Confluence", "confluence.atlassian.com"},
	{" - Figma", "figma.com"},
	{" - Miro", "miro.com"},

	// Research
	{" — Wikipedia", "wikipedia.org"},
	{" - Wikipedia", "wikipedia.org"},
	{" - Google Scholar", "scholar.google.com"},
	{" | arXiv.org", "arxiv.org"},

	// Social
	{" / X", "twitter.com"},
	{" / Twitter", "twitter.com"},
	{" | LinkedIn", "linkedin.com"},
	{" - LinkedIn", "linkedin.com"},
	{" - Reddit", "reddit.com"},
	{" | Hacker News", "news.ycombinator.com"},

	// Entertainment
	{" - YouTube", "youtube.com"},
	{" | Netflix", "netflix.com"},
	{" - Twitch", "twitch.tv"},
	{" - Spotify", "spotify.com"},

	// Google Services
	{" - Google Docs", "docs.google.com"},
	{" - Google Sheets", "docs.google.com"},
	{" - Google Slides", "docs.google.com"},
	{" - Google Drive", "drive.google.com"},
	{" - Google Calendar", "calendar.google.com"},
	{" - Google Meet", "meet.google.com"},
	{" - Google Maps", "maps.google.com"},

	// Cloud Providers
	{" - AWS", "console.aws.amazon.com"},
	{" | Cloud Console", "console.cloud.google.com"},
	{" - Azure Portal", "portal.azure.com"},

	// Finance / Business
	{" - Stripe", "stripe.com"},
	{" - Shopify", "shopify.com"},
	{" | HubSpot", "hubspot.com"},
	{" - Salesforce", "salesforce.com"},

	// Design
	{" – Canva", "canva.com"},
	{" - Adobe", "adobe.com"},
}

// DomainFromTitle attempts to extract a domain from a page title by
// matching known site suffixes. Returns empty string if no match.
func DomainFromTitle(title string) string {
	for _, p := range titleDomainSuffixes {
		if strings.HasSuffix(title, p.suffix) || strings.Contains(title, p.suffix) {
			return p.domain
		}
	}
	return ""
}
