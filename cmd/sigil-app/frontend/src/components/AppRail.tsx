/**
 * AppRail — left-side navigation rail for the Sigil control-plane app.
 *
 * Lists each top-level view as an icon button with a tooltip. The rail is the
 * primary navigation surface; the bottom tab-bar is a compact fallback for
 * narrow viewports. Views are keyed by the same string identifiers as
 * App.tsx's `View` type.
 */

export type RailView =
  | "list"
  | "summary"
  | "timeline"
  | "ask"
  | "plugins"
  | "analytics"
  | "audit"
  | "vm"
  | "team"
  | "settings";

interface RailItem {
  id: RailView;
  label: string;
  icon: preact.ComponentChildren;
}

const SUGGESTIONS_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M9 18h6" />
    <path d="M10 22h4" />
    <path d="M12 2a7 7 0 0 0-4 12.7V17h8v-2.3A7 7 0 0 0 12 2z" />
  </svg>
);

const SUMMARY_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <rect x="3" y="4" width="18" height="16" rx="2" />
    <line x1="8" y1="10" x2="16" y2="10" />
    <line x1="8" y1="14" x2="14" y2="14" />
  </svg>
);

const ASK_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M21 11.5a8.38 8.38 0 0 1-9 8.4 8.5 8.5 0 0 1-4-1L3 21l1.1-5a8.38 8.38 0 0 1-1-4A8.5 8.5 0 0 1 12 3a8.38 8.38 0 0 1 9 8.4z" />
  </svg>
);

const PLUGINS_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M10 2v6h4V2" />
    <path d="M4 10h16v11a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V10z" />
  </svg>
);

const ANALYTICS_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <line x1="18" y1="20" x2="18" y2="10" />
    <line x1="12" y1="20" x2="12" y2="4" />
    <line x1="6" y1="20" x2="6" y2="14" />
  </svg>
);

const AUDIT_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
    <polyline points="14 2 14 8 20 8" />
    <line x1="9" y1="13" x2="15" y2="13" />
    <line x1="9" y1="17" x2="15" y2="17" />
  </svg>
);

const VM_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <rect x="2" y="4" width="20" height="14" rx="2" />
    <line x1="8" y1="22" x2="16" y2="22" />
    <line x1="12" y1="18" x2="12" y2="22" />
    <circle cx="8" cy="11" r="1.5" />
  </svg>
);

const SETTINGS_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
  </svg>
);

const TIMELINE_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <line x1="4" y1="6" x2="20" y2="6" />
    <circle cx="8" cy="6" r="2" fill="currentColor" />
    <line x1="4" y1="12" x2="20" y2="12" />
    <circle cx="14" cy="12" r="2" fill="currentColor" />
    <line x1="4" y1="18" x2="20" y2="18" />
    <circle cx="10" cy="18" r="2" fill="currentColor" />
  </svg>
);

const TEAM_ICON = (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
    <circle cx="9" cy="7" r="4" />
    <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
    <path d="M16 3.13a4 4 0 0 1 0 7.75" />
  </svg>
);

const PRIMARY_ITEMS: RailItem[] = [
  { id: "list", label: "Suggestions", icon: SUGGESTIONS_ICON },
  { id: "summary", label: "Summary", icon: SUMMARY_ICON },
  { id: "timeline", label: "Timeline", icon: TIMELINE_ICON },
  { id: "ask", label: "Ask", icon: ASK_ICON },
  { id: "vm", label: "VM Launcher", icon: VM_ICON },
  { id: "audit", label: "Audit", icon: AUDIT_ICON },
  { id: "plugins", label: "Plugins", icon: PLUGINS_ICON },
  { id: "analytics", label: "Analytics", icon: ANALYTICS_ICON },
  { id: "team", label: "Team", icon: TEAM_ICON },
];

const SECONDARY_ITEMS: RailItem[] = [
  { id: "settings", label: "Settings", icon: SETTINGS_ICON },
];

interface Props {
  activeView: RailView;
  onSelect: (v: RailView) => void;
}

export function AppRail({ activeView, onSelect }: Props) {
  return (
    <nav class="app-rail" aria-label="Primary navigation">
      <div class="app-rail__primary">
        {PRIMARY_ITEMS.map((item) => (
          <RailButton
            key={item.id}
            item={item}
            active={activeView === item.id}
            onSelect={onSelect}
          />
        ))}
      </div>
      <div class="app-rail__secondary">
        {SECONDARY_ITEMS.map((item) => (
          <RailButton
            key={item.id}
            item={item}
            active={activeView === item.id}
            onSelect={onSelect}
          />
        ))}
      </div>
    </nav>
  );
}

function RailButton({
  item,
  active,
  onSelect,
}: {
  item: RailItem;
  active: boolean;
  onSelect: (v: RailView) => void;
}) {
  return (
    <button
      type="button"
      class={`app-rail__btn ${active ? "app-rail__btn--active" : ""}`}
      title={item.label}
      aria-label={item.label}
      aria-current={active ? "page" : undefined}
      onClick={() => onSelect(item.id)}
    >
      <span class="app-rail__icon">{item.icon}</span>
      <span class="app-rail__label">{item.label}</span>
    </button>
  );
}
