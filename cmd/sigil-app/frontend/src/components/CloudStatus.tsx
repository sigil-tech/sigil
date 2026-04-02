import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetCloudStatus(): Promise<any>;
        CloudSignIn(): Promise<void>;
        CloudSignOut(): Promise<void>;
      };
    };
  };
};

const TIER_COLORS: Record<string, string> = {
  free: "var(--fg-secondary)",
  pro: "var(--accent)",
  team: "#8b5cf6",
};

const SYNC_DOTS: Record<string, string> = {
  active: "var(--success)",
  paused: "var(--warning)",
  disabled: "var(--fg-secondary)",
  error: "var(--danger)",
};

export function CloudBadge() {
  const [status, setStatus] = useState<any>(null);

  useEffect(() => {
    window.go.main.App.GetCloudStatus().then(setStatus).catch(() => {});
  }, []);

  if (!status || !status.connected) return null;

  const tier = status.tier || "free";
  const sync = status.sync_state || "disabled";

  return (
    <div class="cloud-badge">
      <span
        class="cloud-sync-dot"
        style={{ background: SYNC_DOTS[sync] || SYNC_DOTS.disabled }}
      />
      <span class="cloud-tier" style={{ color: TIER_COLORS[tier] || TIER_COLORS.free }}>
        {tier}
      </span>
    </div>
  );
}

export function CloudSettings() {
  const [status, setStatus] = useState<any>(null);
  const [loading, setLoading] = useState(false);

  const refresh = () => {
    window.go.main.App.GetCloudStatus().then(setStatus).catch(() => {});
  };

  useEffect(() => {
    refresh();
  }, []);

  const handleSignIn = async () => {
    setLoading(true);
    try {
      await window.go.main.App.CloudSignIn();
      refresh();
    } catch {
      // User may have closed the browser tab.
    } finally {
      setLoading(false);
    }
  };

  const handleSignOut = async () => {
    setLoading(true);
    try {
      await window.go.main.App.CloudSignOut();
      refresh();
    } catch {
      // Daemon unavailable.
    } finally {
      setLoading(false);
    }
  };

  const connected = status?.connected;

  return (
    <div class="cloud-settings">
      {connected ? (
        <div>
          <div class="settings-row">
            <span class="settings-label">Tier</span>
            <span
              class="cloud-tier-badge"
              style={{
                color: TIER_COLORS[status.tier] || TIER_COLORS.free,
              }}
            >
              {status.tier || "Free"}
            </span>
          </div>
          <div class="settings-row">
            <span class="settings-label">Sync</span>
            <span>{status.sync_state || "disabled"}</span>
          </div>
          <div class="settings-row">
            <button
              class="btn daemon-btn daemon-btn-warn"
              onClick={handleSignOut}
              disabled={loading}
            >
              {loading ? "Signing out..." : "Sign Out"}
            </button>
          </div>
        </div>
      ) : (
        <div class="settings-row">
          <span class="settings-label">Not connected</span>
          <button
            class="btn btn-primary"
            onClick={handleSignIn}
            disabled={loading}
          >
            {loading ? "Signing in..." : "Sign In"}
          </button>
        </div>
      )}
    </div>
  );
}
