import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetFleetStatus(): Promise<any>;
        GetFleetPreview(): Promise<any>;
      };
    };
  };
};

export function Team() {
  const [status, setStatus] = useState<any>(null);
  const [preview, setPreview] = useState<any>(null);
  const [showPreview, setShowPreview] = useState(false);
  const [loading, setLoading] = useState(true);

  const refresh = () => {
    setLoading(true);
    window.go.main.App.GetFleetStatus()
      .then(setStatus)
      .catch(() => setStatus(null))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    refresh();
    const interval = setInterval(refresh, 30000);
    return () => clearInterval(interval);
  }, []);

  const handlePreview = async () => {
    try {
      const data = await window.go.main.App.GetFleetPreview();
      setPreview(data);
      setShowPreview(true);
    } catch {
      setPreview(null);
    }
  };

  if (loading) {
    return (
      <div class="team-view">
        <div class="empty-state"><div class="loading-spinner" /></div>
      </div>
    );
  }

  if (!status || !status.active) {
    return (
      <div class="team-view">
        <div class="empty-state">
          <div class="empty-state-title">Team Insights</div>
          <div class="empty-state-text">
            Sign in with a Team or Enterprise account to see team analytics
            and recommendations.
          </div>
        </div>
      </div>
    );
  }

  return (
    <div class="team-view">
      {/* Header */}
      <div class="team-header">
        <div class="team-header-info">
          <h2 class="team-name">{status.team_name || "Team"}</h2>
          <span class="team-org">{status.org_name || ""}</span>
        </div>
        <div class="team-connection">
          <span class={`status-dot ${status.active ? "connected" : "disconnected"}`} />
          <span class="status-text">{status.active ? "Connected" : "Disconnected"}</span>
        </div>
      </div>

      {/* Fleet Status */}
      <div class="team-section">
        <div class="team-section-title">Fleet Status</div>
        <div class="team-status-grid">
          <div class="team-stat">
            <span class="team-stat-label">Node ID</span>
            <span class="team-stat-value team-stat-mono">{status.node_id || "—"}</span>
          </div>
          <div class="team-stat">
            <span class="team-stat-label">Last Sync</span>
            <span class="team-stat-value">
              {status.last_sent ? new Date(status.last_sent).toLocaleTimeString() : "Never"}
            </span>
          </div>
          <div class="team-stat">
            <span class="team-stat-label">Queue</span>
            <span class="team-stat-value">{status.queue_size || 0} pending</span>
          </div>
          <div class="team-stat">
            <span class="team-stat-label">Role</span>
            <span class="team-stat-value">{status.role || "member"}</span>
          </div>
        </div>
      </div>

      {/* Preview Report */}
      <div class="team-section">
        <button type="button" class="btn daemon-btn" onClick={handlePreview}>
          {showPreview ? "Hide Report Preview" : "Preview Report Data"}
        </button>
        {showPreview && preview && (
          <pre class="team-preview">
            {JSON.stringify(preview, null, 2)}
          </pre>
        )}
      </div>
    </div>
  );
}
