import { useEffect, useState, useCallback } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        VMStart(req: VMStartRequest): Promise<any>;
        VMStop(sessionID: string): Promise<void>;
        VMStatus(sessionID: string): Promise<any>;
        VMList(limit: number): Promise<any[]>;
        VMMerge(sessionID: string): Promise<any>;
      };
    };
  };
  runtime: {
    EventsOn(event: string, cb: (...args: any[]) => void): () => void;
  };
};

interface VMStartRequest {
  disk_image_path: string;
  overlay_path?: string;
  vm_db_path?: string;
  vsock_cid?: number;
  filter_version?: string;
}

type LifecycleState =
  | "booting"
  | "ready"
  | "connecting"
  | "stopping"
  | "stopped"
  | "failed";

type MergeOutcome =
  | "pending"
  | "complete"
  | "partial"
  | "failed"
  | "skipped";

interface Session {
  id: string;
  started_at: string;
  ended_at?: string;
  status: LifecycleState;
  merge_outcome: MergeOutcome;
  disk_image_path: string;
  overlay_path?: string;
  vm_db_path?: string;
  vsock_cid?: number;
  filter_version?: string;
}

const ACTIVE_STATES: LifecycleState[] = [
  "booting",
  "ready",
  "connecting",
  "stopping",
];

const STATE_LABEL: Record<LifecycleState, string> = {
  booting: "Booting",
  ready: "Ready",
  connecting: "Connecting",
  stopping: "Stopping",
  stopped: "Stopped",
  failed: "Failed",
};

function isActive(s: LifecycleState): boolean {
  return ACTIVE_STATES.includes(s);
}

function formatDuration(startISO: string, endISO?: string): string {
  if (!startISO) return "-";
  const start = new Date(startISO).getTime();
  const end = endISO ? new Date(endISO).getTime() : Date.now();
  const secs = Math.floor((end - start) / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ${secs % 60}s`;
  const hrs = Math.floor(mins / 60);
  return `${hrs}h ${mins % 60}m`;
}

function formatTime(iso?: string): string {
  if (!iso) return "-";
  return new Date(iso).toLocaleString();
}

export function VMLauncher() {
  const [active, setActive] = useState<Session | null>(null);
  const [history, setHistory] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [launching, setLaunching] = useState(false);
  const [diskImagePath, setDiskImagePath] = useState("");

  const refresh = useCallback(async () => {
    try {
      const [status, list] = await Promise.all([
        window.go.main.App.VMStatus(""),
        window.go.main.App.VMList(20),
      ]);
      if (status && isActive(status.status)) {
        setActive(status);
      } else {
        setActive(null);
      }
      setHistory(list || []);
      setError(null);
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Poll while an active session exists, and subscribe to vm-status push
  // events for sub-second updates when available.
  useEffect(() => {
    if (!active) return;
    const id = setInterval(refresh, 3000);
    return () => clearInterval(id);
  }, [active, refresh]);

  useEffect(() => {
    if (!window.runtime?.EventsOn) return;
    const off = window.runtime.EventsOn("vm-status", () => {
      refresh();
    });
    return () => {
      off?.();
    };
  }, [refresh]);

  const handleLaunch = async () => {
    if (!diskImagePath.trim()) {
      setError("Please provide a disk image path.");
      return;
    }
    setLaunching(true);
    setError(null);
    try {
      const sess = await window.go.main.App.VMStart({
        disk_image_path: diskImagePath.trim(),
      });
      setActive(sess);
      await refresh();
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setLaunching(false);
    }
  };

  const handleStop = async () => {
    if (!active) return;
    setError(null);
    try {
      await window.go.main.App.VMStop(active.id);
      await refresh();
    } catch (e: any) {
      setError(e?.message || String(e));
    }
  };

  const handleMerge = async (sessionID: string) => {
    setError(null);
    try {
      await window.go.main.App.VMMerge(sessionID);
      await refresh();
    } catch (e: any) {
      setError(e?.message || String(e));
    }
  };

  const bootProgress = (() => {
    if (!active) return 0;
    switch (active.status) {
      case "booting":
        return 35;
      case "connecting":
        return 70;
      case "ready":
        return 100;
      case "stopping":
        return 100;
      default:
        return 0;
    }
  })();

  return (
    <div class="vm-launcher">
      <header class="vm-header">
        <h1>VM Launcher</h1>
        <p class="subtitle">
          Launch an ephemeral VM sandbox. AI tools run inside the VM; the merge
          API is the only trust boundary between VM and host.
        </p>
      </header>

      {error && <div class="alert alert-error">{error}</div>}
      {loading && <p class="muted">Loading VM state…</p>}

      <section class="vm-active">
        <h2>Active session</h2>
        {!active ? (
          <div class="vm-no-active">
            <p class="muted">No active session.</p>
            <div class="vm-launch-form">
              <label for="disk-image-path">Disk image path</label>
              <input
                id="disk-image-path"
                type="text"
                value={diskImagePath}
                onInput={(e) =>
                  setDiskImagePath((e.target as HTMLInputElement).value)
                }
                placeholder="~/.local/share/sigild/vm/base.qcow2"
                disabled={launching}
              />
              <button
                class="btn-primary"
                onClick={handleLaunch}
                disabled={launching || !diskImagePath.trim()}
              >
                {launching ? "Launching…" : "Launch VM"}
              </button>
            </div>
          </div>
        ) : (
          <div class="vm-session-card">
            <div class="vm-status-row">
              <div>
                <div class="vm-session-id">{active.id.substring(0, 12)}…</div>
                <div class="muted">
                  Started {formatTime(active.started_at)} · uptime{" "}
                  {formatDuration(active.started_at)}
                </div>
              </div>
              <span
                class={`vm-badge vm-badge--${active.status}`}
                title={STATE_LABEL[active.status]}
              >
                {STATE_LABEL[active.status]}
              </span>
            </div>

            <div class="vm-progress">
              <div class="vm-progress-label">
                {active.status === "stopping" ? "Merge in progress" : "Boot progress"}
              </div>
              <div class="vm-progress-bar">
                <div
                  class={`vm-progress-fill vm-progress-fill--${active.status}`}
                  style={{ width: `${bootProgress}%` }}
                />
              </div>
            </div>

            <dl class="vm-detail">
              <div>
                <dt>Disk image</dt>
                <dd class="mono">{active.disk_image_path || "-"}</dd>
              </div>
              <div>
                <dt>Overlay</dt>
                <dd class="mono">{active.overlay_path || "-"}</dd>
              </div>
              <div>
                <dt>vsock CID</dt>
                <dd class="mono">{active.vsock_cid ?? "-"}</dd>
              </div>
              <div>
                <dt>Filter</dt>
                <dd class="mono">{active.filter_version || "-"}</dd>
              </div>
            </dl>

            <div class="vm-actions">
              <button
                class="btn-danger"
                onClick={handleStop}
                disabled={active.status === "stopping"}
              >
                Stop VM
              </button>
              <button class="btn-secondary" onClick={refresh}>
                Refresh
              </button>
            </div>
          </div>
        )}
      </section>

      <section class="vm-history">
        <h2>Recent sessions</h2>
        <table class="vm-table">
          <thead>
            <tr>
              <th>Session</th>
              <th>Started</th>
              <th>Ended</th>
              <th>Status</th>
              <th>Merge</th>
              <th>Duration</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {history.length === 0 && (
              <tr>
                <td colSpan={7} class="empty">
                  No sessions yet.
                </td>
              </tr>
            )}
            {history.map((s) => (
              <tr key={s.id}>
                <td class="mono">{s.id.substring(0, 12)}…</td>
                <td>{formatTime(s.started_at)}</td>
                <td>{formatTime(s.ended_at)}</td>
                <td>
                  <span class={`vm-badge vm-badge--${s.status}`}>
                    {STATE_LABEL[s.status]}
                  </span>
                </td>
                <td>
                  <span class={`vm-merge vm-merge--${s.merge_outcome}`}>
                    {s.merge_outcome}
                  </span>
                </td>
                <td>{formatDuration(s.started_at, s.ended_at)}</td>
                <td>
                  {s.status === "stopped" &&
                    s.merge_outcome === "pending" && (
                      <button
                        class="btn-link"
                        onClick={() => handleMerge(s.id)}
                      >
                        Merge
                      </button>
                    )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}
