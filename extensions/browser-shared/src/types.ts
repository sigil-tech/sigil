// BrowserEvent is the envelope sent to sigild's plugin ingest endpoint.
// Mirrors the Go plugin.Event structure in internal/plugin/plugin.go.

export interface BrowserEventPayload {
  action:
    | "tab_activated"
    | "tab_created"
    | "tab_closed"
    | "tab_updated"
    | "tab_count"
    | "page_time";
  browser: "chrome" | "firefox";
  tab_id?: number;
  page_title?: string;
  domain?: string;
  category?: DomainCategory;
  tab_count?: number;
  active_seconds?: number;
  previous_domain?: string;
}

export interface BrowserEvent {
  plugin: string; // "chrome-extension" | "firefox-extension"
  kind: string; // "browser"
  timestamp: string; // ISO 8601
  payload: BrowserEventPayload;
}

export type DomainCategory =
  | "development"
  | "documentation"
  | "communication"
  | "project_management"
  | "research"
  | "social"
  | "entertainment"
  | "meeting"
  | "other";
