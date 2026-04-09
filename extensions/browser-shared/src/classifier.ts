// Domain classifier — maps domains to workflow categories.
// Categories match the Go-side browser source in internal/collector/sources/.

import type { DomainCategory } from "./types";

// Exact domain matches.
const EXACT_MAP: Record<string, DomainCategory> = {
  // Development
  "github.com": "development",
  "gitlab.com": "development",
  "bitbucket.org": "development",
  "stackoverflow.com": "development",
  "stackexchange.com": "development",
  "npmjs.com": "development",
  "crates.io": "development",
  "pypi.org": "development",
  "pkg.go.dev": "development",
  "hub.docker.com": "development",
  "codepen.io": "development",
  "codesandbox.io": "development",
  "replit.com": "development",
  "vercel.com": "development",
  "netlify.com": "development",
  "heroku.com": "development",
  "railway.app": "development",
  "render.com": "development",
  "fly.io": "development",
  "console.aws.amazon.com": "development",
  "portal.azure.com": "development",
  "console.cloud.google.com": "development",

  // Communication
  "mail.google.com": "communication",
  "outlook.live.com": "communication",
  "outlook.office.com": "communication",
  "slack.com": "communication",
  "teams.microsoft.com": "communication",
  "discord.com": "communication",
  "web.whatsapp.com": "communication",
  "web.telegram.org": "communication",
  "app.element.io": "communication",

  // Project Management
  "notion.so": "project_management",
  "linear.app": "project_management",
  "trello.com": "project_management",
  "asana.com": "project_management",
  "Monday.com": "project_management",
  "clickup.com": "project_management",
  "basecamp.com": "project_management",
  "shortcut.com": "project_management",
  "height.app": "project_management",

  // Research
  "wikipedia.org": "research",
  "arxiv.org": "research",
  "scholar.google.com": "research",
  "semanticscholar.org": "research",
  "researchgate.net": "research",
  "pubmed.ncbi.nlm.nih.gov": "research",
  "wolframalpha.com": "research",

  // Social
  "twitter.com": "social",
  "x.com": "social",
  "linkedin.com": "social",
  "reddit.com": "social",
  "facebook.com": "social",
  "instagram.com": "social",
  "mastodon.social": "social",
  "bsky.app": "social",
  "news.ycombinator.com": "social",

  // Entertainment
  "youtube.com": "entertainment",
  "netflix.com": "entertainment",
  "twitch.tv": "entertainment",
  "spotify.com": "entertainment",
  "music.apple.com": "entertainment",
  "soundcloud.com": "entertainment",
  "disneyplus.com": "entertainment",
  "hulu.com": "entertainment",
  "primevideo.com": "entertainment",

  // Meeting
  "meet.google.com": "meeting",
  "zoom.us": "meeting",
  "app.zoom.us": "meeting",

  // Documentation
  "react.dev": "documentation",
  "vuejs.org": "documentation",
  "angular.io": "documentation",
  "svelte.dev": "documentation",
  "nextjs.org": "documentation",
  "go.dev": "documentation",
  "doc.rust-lang.org": "documentation",
  "docs.python.org": "documentation",
  "typescriptlang.org": "documentation",
  "kotlinlang.org": "documentation",
  "learn.microsoft.com": "documentation",
  "nodejs.org": "documentation",
  "deno.land": "documentation",
  "bun.sh": "documentation",
  "tailwindcss.com": "documentation",
  "mdn.io": "documentation",
};

// Subdomain prefix rules: if the hostname starts with one of these prefixes
// followed by a dot, it matches the category.
const PREFIX_RULES: { prefix: string; category: DomainCategory }[] = [
  { prefix: "docs", category: "documentation" },
  { prefix: "developer", category: "documentation" },
  { prefix: "devdocs", category: "documentation" },
  { prefix: "wiki", category: "documentation" },
  { prefix: "api", category: "documentation" },
];

// Suffix rules: if the hostname or parent domain matches.
const SUFFIX_RULES: { suffix: string; category: DomainCategory }[] = [
  { suffix: ".atlassian.net", category: "project_management" },
  { suffix: ".jira.com", category: "project_management" },
  { suffix: ".atlassian.com", category: "project_management" },
  { suffix: ".slack.com", category: "communication" },
  { suffix: ".zoom.us", category: "meeting" },
  { suffix: ".github.io", category: "documentation" },
  { suffix: ".readthedocs.io", category: "documentation" },
  { suffix: ".readthedocs.org", category: "documentation" },
  { suffix: ".gitbook.io", category: "documentation" },
];

/**
 * Classify a domain into a workflow category.
 * Returns "other" for unknown domains.
 */
export function classifyDomain(domain: string): DomainCategory {
  if (!domain) return "other";
  const lower = domain.toLowerCase();

  // 1. Exact match.
  if (EXACT_MAP[lower]) return EXACT_MAP[lower];

  // 2. Check without leading "www.".
  const bare = lower.startsWith("www.") ? lower.slice(4) : lower;
  if (EXACT_MAP[bare]) return EXACT_MAP[bare];

  // 3. Prefix rules (e.g. docs.example.com -> documentation).
  for (const rule of PREFIX_RULES) {
    if (lower.startsWith(rule.prefix + ".")) {
      return rule.category;
    }
  }

  // 4. Suffix rules (e.g. *.atlassian.net -> project_management).
  for (const rule of SUFFIX_RULES) {
    if (lower.endsWith(rule.suffix)) {
      return rule.category;
    }
  }

  // 5. developer.mozilla.org special case (MDN).
  if (lower === "developer.mozilla.org") return "documentation";

  return "other";
}
