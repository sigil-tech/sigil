# 026 — Fleet Integration: Team Intelligence Platform

**Status:** Draft
**Author:** Alec Feeman
**Date:** 2026-04-09

---

## Problem

Fleet exists as a separate service (sigil-tech/sigil-fleet) but the sigild daemon's connection to it was ripped out when fleet was extracted into its own repo. The daemon currently has stub fleet handlers that return config values but don't send any data. Users on Team and Enterprise plans have no way to:

1. Authenticate their daemon to fleet via the cloud auth system (Zitadel)
2. Send telemetry data from their team
3. View team-level analytics and insights
4. Receive team/org-level recommendations from ML models

Meanwhile, fleet's service has basic API-key auth and OIDC stub code, but no real integration with the Sigil Cloud auth provider. The dashboard exists but can't authenticate users.

## Goals

1. **Connect sigild to fleet via cloud auth** — when a user signs in to Sigil Cloud (Team/Enterprise tier), the daemon automatically authenticates to fleet using the same OAuth token. No separate fleet setup needed.

2. **Send enriched telemetry** — fleet's report format is outdated (missing the 14 new event kinds from spec 023). Update the reporter to include knowledge worker signals: browser time distribution, idle patterns, meeting load, focus scores.

3. **Team and org-level models** — fleet aggregates individual telemetry into team patterns. Fleet's ML pipeline trains models on team data to answer: "When is the team most productive?", "Which meetings are most disruptive?", "How does onboarding time correlate with tooling adoption?"

4. **Team recommendations** — fleet pushes insights back to individual daemons via the routing policy mechanism. Example: "Your team's focus time drops 40% on meeting-heavy Tuesdays — consider blocking Tuesday mornings."

5. **Privacy preserved** — fleet never receives raw events, file paths, URLs, or content. Only anonymized aggregate metrics. Users can preview exactly what will be sent.

## Non-Goals

- Individual performance tracking or comparison
- Real-time event streaming to fleet (batch aggregates only)
- Replacing the local ML pipeline (fleet ML supplements, not replaces)
- Building a fleet admin console (dashboard exists, needs auth + new views)

---

## Current State

### sigil repo (this repo)

- `cmd/sigild/main.go`: fleet handlers are stubs — `fleet-preview` returns config, `fleet-opt-out` is a no-op
- `internal/config/config.go`: `FleetConfig` has `Enabled`, `Endpoint`, `Interval`, `NodeID`
- Fleet was previously an imported Go library; now it's a separate service
- Cloud auth exists in the desktop app (`CloudSignIn` → OAuth) but daemon has no auth token

### sigil-fleet repo

- `reporter.go`: computes `FleetReport` from local SQLite via `EventReader` interface
- `service/`: HTTP service with PostgreSQL storage, API-key + OIDC stub auth
- `service/schema.sql`: `orgs`, `teams`, `nodes`, `daily_metrics` tables
- `dashboard/`: React dashboard with auth stub and metric views
- `types.go`: `FleetReport` with 17 fields (missing new event kinds)

### What's missing

| Component | Location | Gap |
|-----------|----------|-----|
| Daemon → Fleet auth | sigild | No OAuth token, only API key stub |
| Report enrichment | sigil-fleet reporter.go | Missing 14 new event kinds |
| Fleet API auth | sigil-fleet service/auth.go | OIDC userinfo check, no Zitadel integration |
| Team/org enrollment | sigil-fleet service/ | No enrollment API |
| Team ML models | sigil-fleet (new) | Doesn't exist |
| Team recommendations | sigil-fleet → sigild | Policy mechanism exists but only for routing |
| Dashboard auth | sigil-fleet dashboard/ | Stub, no real login flow |
| Desktop app fleet UI | sigil cmd/sigil-app/ | Cloud section exists, no fleet dashboard |

---

## Design

### Architecture

```
Individual Daemon (sigild)
    │
    │ OAuth token from Sigil Cloud sign-in
    │
    ├──► Fleet Reporter (runs in daemon)
    │    Computes anonymized hourly aggregate from local SQLite
    │    Sends FleetReport to fleet API
    │
    ▼
Fleet Service (sigil-fleet)
    │
    │ Validates OAuth token → extracts org_id, team_id
    │ Stores report in daily_metrics with org/team attribution
    │
    ├──► Aggregation Engine
    │    Computes team/org level metrics from individual reports
    │    Trains team ML models on aggregated data
    │
    ├──► Recommendation Engine
    │    Generates team-level insights from models
    │    Pushes recommendations via policy endpoint
    │
    ├──► Fleet Dashboard
    │    Team leads view team metrics, trends, recommendations
    │    Authenticated via same Sigil Cloud OAuth
    │
    └──► Policy API
         Returns routing policy + team recommendations to daemons
```

### Part 1: Auth Flow (both repos)

**sigil repo changes:**

When user signs in to Sigil Cloud (existing `CloudSignIn` OAuth flow), the daemon receives an access token. This token is stored in the config and used for fleet API calls.

```
User clicks "Sign In" in Sigil app
    → Browser opens Zitadel login page
    → User authenticates (email/SSO)
    → Callback with access_token + refresh_token
    → sigild stores tokens in config.toml [cloud] section
    → Fleet reporter uses access_token as Bearer auth
    → Token refresh handled automatically
```

**sigil-fleet repo changes:**

Replace API-key auth with Zitadel JWT validation:

```
Fleet service receives report with Bearer token
    → Validate JWT signature against Zitadel JWKS
    → Extract claims: sub (user_id), org_id, team_id, tier
    → Reject if tier < "team" (fleet is Team/Enterprise only)
    → Associate report with org_id and team_id
    → Store in daily_metrics with org/team attribution
```

### Part 2: Enriched Telemetry (sigil-fleet)

Update `FleetReport` with new fields from spec 023:

```go
type FleetReport struct {
    // ... existing fields ...

    // Knowledge worker signals (spec 023)
    IdleMinutes       float64            `json:"idle_minutes"`
    ActiveMinutes     float64            `json:"active_minutes"`
    MeetingMinutes    float64            `json:"meeting_minutes"`
    BrowserCategories map[string]float64 `json:"browser_categories"` // category → minutes
    FocusScore        float64            `json:"focus_score"`        // 0-100
    ContextSwitches   int                `json:"context_switches"`
    TopApps           map[string]float64 `json:"top_apps"`           // app → minutes

    // Never sent: raw events, file paths, URLs, domains, content
}
```

Update the reporter to compute these from the new event kinds:
- `IdleMinutes`: sum of idle_end durations
- `ActiveMinutes`: total time minus idle minus meeting
- `MeetingMinutes`: sum of calendar meeting durations
- `BrowserCategories`: time per category from browser events
- `FocusScore`: ML-computed or heuristic (no context switches > 30min = high focus)
- `ContextSwitches`: count of app focus changes
- `TopApps`: time per app from focus events (app names, not window titles)

### Part 3: Team Enrollment (sigil-fleet)

New API endpoints:

```
POST /api/v1/enroll          ← daemon calls on first fleet connect
  Request:  { node_id, platform, version }
  Response: { org_id, team_id, org_name, team_name }
  (org_id and team_id extracted from JWT claims)

GET  /api/v1/team/members    ← dashboard lists team members
  Response: { members: [{ node_id, last_seen, platform }] }

POST /api/v1/team/invite     ← admin invites users to team
  Request:  { email }
```

### Part 4: Team ML Models (sigil-fleet — new)

Fleet aggregates individual reports into team-level datasets and trains models:

**Team Focus Model:**
- Input: hourly reports from all team members
- Output: team focus score by hour, day, week
- Features: meeting density, context-switch rate, active coding ratio
- Training: weekly retrain on last 30 days of team data

**Meeting Impact Model:**
- Input: team metrics before/after meetings
- Output: meeting disruption score per meeting type
- Features: focus drop duration, recovery time, context switches post-meeting

**Onboarding Model:**
- Input: new member metrics over first 90 days
- Output: predicted ramp-up time, tooling adoption curve
- Features: event volume growth, suggestion acceptance rate, task completion velocity

### Part 5: Team Recommendations (sigil-fleet → sigild)

Fleet pushes team-level insights via the existing policy endpoint:

```
GET /api/v1/policy?node_id=xxx
Response: {
    "routing_mode": "localfirst",
    "recommendations": [
        {
            "type": "team_insight",
            "title": "Meeting-heavy Tuesday",
            "body": "Your team's focus time drops 40% on Tuesdays. Consider blocking Tuesday mornings for deep work.",
            "confidence": 0.85,
            "action": "suggest_calendar_block"
        },
        {
            "type": "team_insight",
            "title": "Peak productivity window",
            "body": "Your team ships 3x more code between 9-11am. Protect this window from meetings.",
            "confidence": 0.92
        }
    ]
}
```

The daemon polls the policy endpoint (already exists) and surfaces team recommendations via the notifier alongside individual suggestions.

### Part 6: Dashboard (sigil-fleet)

Update the existing React dashboard with:

1. **Zitadel OAuth login** (replace stub)
2. **Team Overview**: team focus score, active members, event volume trends
3. **Meeting Analytics**: meeting load by day, disruption scores, recommended focus blocks
4. **Productivity Patterns**: peak hours, context-switch hotspots, browser time distribution
5. **ML Insights**: team recommendations, model confidence scores, trend predictions
6. **Privacy Controls**: preview what data leaves each node, opt-out per member

---

## Multi-Repo Spec Breakdown

This is too large for a single spec. It breaks into these work items:

### sigil repo (this repo)

**Spec 026a — Fleet Reporter Reconnection**
- Re-import sigil-fleet as a Go module (now that repo is accessible)
- OR: implement a thin HTTP reporter directly in sigild that POSTs FleetReport to the fleet API
- Wire cloud auth token into fleet API calls
- Enrich FleetReport with spec 023 signal data
- Update fleet socket handlers to be functional again

**Spec 026b — Team Recommendations in Desktop App**
- Poll fleet policy endpoint for team recommendations
- Surface team insights alongside individual suggestions in the notifier
- Show team recommendations in the desktop app's suggestion list
- Settings: fleet connection status, data preview, opt-out

### sigil-fleet repo

**Spec F001 — Auth Migration to Zitadel**
- Replace API-key + OIDC stub with Zitadel JWT validation
- JWKS endpoint caching
- Claims extraction (org_id, team_id, tier)
- Tier gating (Team/Enterprise only)

**Spec F002 — Enrollment + Team Management**
- Enrollment endpoint (node registers with fleet)
- Team member listing
- Invite flow
- Node-to-org/team association from JWT claims

**Spec F003 — Enriched Telemetry Schema**
- Update daily_metrics schema with new columns
- Migrate existing data
- Update report ingest handler
- Add knowledge worker signal fields to FleetReport

**Spec F004 — Team ML Models**
- Team focus model (hourly aggregation → focus score)
- Meeting impact model
- Onboarding model
- Weekly retrain pipeline
- Model serving via fleet API

**Spec F005 — Team Recommendations Engine**
- Generate recommendations from model outputs
- Push via policy endpoint
- Confidence scoring
- Recommendation deduplication and rate limiting

**Spec F006 — Dashboard Auth + New Views**
- Zitadel OAuth login flow
- Team overview, meeting analytics, productivity patterns
- ML insights view
- Privacy controls and data preview

---

## Privacy Considerations

Fleet telemetry follows the existing privacy model:

1. **Aggregates only** — never raw events, file paths, URLs, or content
2. **App names, not window titles** — "Chrome 45min" not "Pull request #76"
3. **Categories, not domains** — "documentation 30min" not "react.dev 30min"
4. **No individual comparison** — team metrics only, no leaderboards
5. **Preview before send** — users see exactly what will be transmitted
6. **Opt-out per member** — any team member can disable fleet without affecting their local Sigil
7. **Data retention** — fleet data purged after 90 days (configurable by org admin)

---

## Implementation Priority

| Spec | Repo | What | Effort | Depends on |
|------|------|------|--------|-----------|
| 026a | sigil | Fleet reporter + auth | 3-4 days | Zitadel setup |
| F001 | fleet | Zitadel auth | 2-3 days | Zitadel setup |
| F003 | fleet | Enriched schema | 1-2 days | 026a |
| F002 | fleet | Enrollment | 2-3 days | F001 |
| 026b | sigil | Team recommendations UI | 2-3 days | F005 |
| F005 | fleet | Recommendations engine | 3-4 days | F003, F004 |
| F004 | fleet | Team ML models | 5-7 days | F003 |
| F006 | fleet | Dashboard | 3-5 days | F001 |

**Total: ~22-31 days across both repos.**

Critical path: Zitadel setup → F001 + 026a (parallel) → F003 → F004 → F005 → 026b
