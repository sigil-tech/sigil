import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        CheckAccessibility(): Promise<boolean>;
        PromptAccessibility(): Promise<void>;
      };
    };
  };
};

interface PermissionRow {
  name: string;
  description: string;
  unlocks: string;
  granted: boolean | null;
  onGrant: () => void;
}

export function Permissions() {
  const [accessibility, setAccessibility] = useState<boolean | null>(null);
  const [checking, setChecking] = useState(false);

  const refresh = () => {
    window.go.main.App.CheckAccessibility()
      .then(setAccessibility)
      .catch(() => setAccessibility(null));
  };

  useEffect(() => {
    refresh();
  }, []);

  const handleGrantAccessibility = async () => {
    setChecking(true);
    await window.go.main.App.PromptAccessibility();
    // Re-check periodically — user needs to toggle the switch in System Settings.
    const interval = setInterval(() => {
      window.go.main.App.CheckAccessibility().then((granted) => {
        setAccessibility(granted);
        if (granted) clearInterval(interval);
      });
    }, 2000);
    // Stop polling after 60 seconds.
    setTimeout(() => {
      clearInterval(interval);
      setChecking(false);
    }, 60000);
  };

  const rows: PermissionRow[] = [
    {
      name: "Window Tracking",
      description: "See which document or file you're working on",
      unlocks:
        "Window titles, browser page titles, document names in Excel/Mail",
      granted: accessibility,
      onGrant: handleGrantAccessibility,
    },
  ];

  // Only show on macOS — other platforms don't need these permissions.
  const isMac =
    typeof navigator !== "undefined" && /Mac/.test(navigator.platform);
  if (!isMac) return null;

  return (
    <div class="permissions-section">
      {rows.map((row) => (
        <div key={row.name} class="settings-row">
          <div class="settings-label-group">
            <span class="settings-label">{row.name}</span>
            <div class="settings-label-sub">{row.description}</div>
          </div>
          {row.granted === true ? (
            <span class="permission-granted">Enabled</span>
          ) : (
            <button
              type="button"
              class="btn btn-primary"
              onClick={row.onGrant}
              disabled={checking}
              style={{ fontSize: "12px", padding: "5px 12px" }}
            >
              {checking ? "Waiting..." : "Enable"}
            </button>
          )}
        </div>
      ))}
      {accessibility === true && (
        <div class="settings-row">
          <div class="settings-label-sub" style={{ color: "var(--success)" }}>
            Sigil can read window titles — browser pages, document names, and
            app context are now captured.
          </div>
        </div>
      )}
      {accessibility === false && (
        <div class="settings-row">
          <div class="settings-label-sub">
            Without this, Sigil only sees which app is focused — not what
            you're working on inside it.
          </div>
        </div>
      )}
    </div>
  );
}
