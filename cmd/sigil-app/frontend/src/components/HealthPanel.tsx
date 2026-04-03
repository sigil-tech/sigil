import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetHealth(): Promise<{
          services: {
            name: string;
            status: string;
            message: string;
            fix?: string;
          }[];
        }>;
      };
    };
  };
};

const STATUS_DOT: Record<string, string> = {
  ok: "var(--success)",
  degraded: "var(--warning)",
  down: "var(--danger)",
  disabled: "var(--fg-secondary)",
};

const STATUS_LABEL: Record<string, string> = {
  ok: "Healthy",
  degraded: "Degraded",
  down: "Down",
  disabled: "Disabled",
};

export function HealthPanel() {
  const [services, setServices] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = () => {
    setLoading(true);
    window.go.main.App.GetHealth()
      .then((r) => setServices(r.services || []))
      .catch(() => setServices([]))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    refresh();
    // Refresh every 30 seconds.
    const interval = setInterval(refresh, 30000);
    return () => clearInterval(interval);
  }, []);

  // Don't show if everything is OK or loading.
  if (loading) return null;

  const hasIssues = services.some(
    (s) => s.status === "down" || s.status === "degraded"
  );

  return (
    <div class={`health-panel ${hasIssues ? "has-issues" : ""}`}>
      {services.map((svc) => (
        <div key={svc.name} class="health-row">
          <span
            class="health-dot"
            style={{ background: STATUS_DOT[svc.status] || STATUS_DOT.disabled }}
          />
          <span class="health-name">{svc.name}</span>
          <span class={`health-status health-${svc.status}`}>
            {STATUS_LABEL[svc.status] || svc.status}
          </span>
        </div>
      ))}
      {hasIssues && (
        <div class="health-details">
          {services
            .filter((s) => s.status === "down" || s.status === "degraded")
            .map((svc) => (
              <div key={svc.name} class="health-issue">
                <div class="health-issue-msg">{svc.message}</div>
                {svc.fix && <div class="health-issue-fix">{svc.fix}</div>}
              </div>
            ))}
        </div>
      )}
    </div>
  );
}
