import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetCorpusStats(): Promise<any>;
        GetAuditCorpus(limit: number): Promise<any[]>;
        GetAuditMergeLog(): Promise<any[]>;
        GetAuditFilteredLog(): Promise<any[]>;
        PurgeCorpusSession(beforeTS: number): Promise<any>;
      };
    };
  };
};

type Tab = "corpus" | "merge" | "filtered";

interface CorpusStats {
  total_rows: number;
  rows_by_origin: Record<string, number>;
  label_distribution: Record<string, number>;
  annotated_count: number;
  unannotated_count: number;
  oldest_ts: number;
  newest_ts: number;
}

interface CorpusRow {
  id: number;
  ts: number;
  origin: string;
  event_type: string;
  source: string;
  payload_hash: string;
  label?: string;
  phase?: string;
  confidence?: number;
}

interface MergeEntry {
  session_id: string;
  started_at: number;
  completed_at?: number;
  status: string;
  rows_merged: number;
  rows_filtered: number;
  error?: string;
}

interface FilterEntry {
  session_id: string;
  ts: number;
  event_type: string;
  filter_rule: string;
  excluded_reason: string;
  payload_hash: string;
}

function formatDate(ms: number): string {
  if (!ms || ms === 0) return "-";
  return new Date(ms).toLocaleString();
}

export function Audit() {
  const [activeTab, setActiveTab] = useState<Tab>("corpus");
  const [stats, setStats] = useState<CorpusStats | null>(null);
  const [corpusRows, setCorpusRows] = useState<CorpusRow[]>([]);
  const [mergeRows, setMergeRows] = useState<MergeEntry[]>([]);
  const [filterRows, setFilterRows] = useState<FilterEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    loadData();
  }, [activeTab]);

  const loadData = async () => {
    setLoading(true);
    setError(null);
    try {
      if (activeTab === "corpus") {
        const [s, r] = await Promise.all([
          window.go.main.App.GetCorpusStats(),
          window.go.main.App.GetAuditCorpus(100),
        ]);
        setStats(s);
        setCorpusRows(r || []);
      } else if (activeTab === "merge") {
        const rows = await window.go.main.App.GetAuditMergeLog();
        setMergeRows(rows || []);
      } else if (activeTab === "filtered") {
        const rows = await window.go.main.App.GetAuditFilteredLog();
        setFilterRows(rows || []);
      }
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div class="audit-view">
      <h1>Audit Viewer</h1>
      <p class="subtitle">
        Inspect what Sigil stores locally. Only metadata and HMAC-keyed hashes
        are shown — raw payloads are never displayed.
      </p>

      <div class="tabs">
        <button
          class={activeTab === "corpus" ? "tab active" : "tab"}
          onClick={() => setActiveTab("corpus")}
        >
          Training Corpus
        </button>
        <button
          class={activeTab === "merge" ? "tab active" : "tab"}
          onClick={() => setActiveTab("merge")}
        >
          Merge Log
        </button>
        <button
          class={activeTab === "filtered" ? "tab active" : "tab"}
          onClick={() => setActiveTab("filtered")}
        >
          Filtered Log
        </button>
      </div>

      {loading && <p>Loading…</p>}
      {error && <p class="error">Error: {error}</p>}

      {activeTab === "corpus" && stats && (
        <div class="audit-corpus">
          <div class="stats-grid">
            <div class="stat">
              <div class="stat-label">Total rows</div>
              <div class="stat-value">{stats.total_rows}</div>
            </div>
            <div class="stat">
              <div class="stat-label">Annotated</div>
              <div class="stat-value">{stats.annotated_count}</div>
            </div>
            <div class="stat">
              <div class="stat-label">Unannotated</div>
              <div class="stat-value">{stats.unannotated_count}</div>
            </div>
          </div>

          {stats.rows_by_origin && Object.keys(stats.rows_by_origin).length > 0 && (
            <div class="stat-block">
              <h3>By origin</h3>
              <ul>
                {Object.entries(stats.rows_by_origin).map(([k, v]) => (
                  <li>
                    <strong>{k}</strong>: {v}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {stats.label_distribution && Object.keys(stats.label_distribution).length > 0 && (
            <div class="stat-block">
              <h3>Label distribution</h3>
              <ul>
                {Object.entries(stats.label_distribution).map(([k, v]) => (
                  <li>
                    <strong>{k}</strong>: {v}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <h3>Recent rows (metadata only)</h3>
          <table class="audit-table">
            <thead>
              <tr>
                <th>ID</th>
                <th>Time</th>
                <th>Origin</th>
                <th>Event</th>
                <th>Label</th>
                <th>Phase</th>
                <th>Confidence</th>
                <th>Hash</th>
              </tr>
            </thead>
            <tbody>
              {corpusRows.map((r) => (
                <tr key={r.id}>
                  <td>{r.id}</td>
                  <td>{formatDate(r.ts)}</td>
                  <td>{r.origin}</td>
                  <td>{r.event_type}</td>
                  <td>{r.label || "-"}</td>
                  <td>{r.phase || "-"}</td>
                  <td>{r.confidence != null ? r.confidence.toFixed(2) : "-"}</td>
                  <td class="hash">{r.payload_hash.substring(0, 12)}…</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {activeTab === "merge" && (
        <div class="audit-merge">
          <h3>Merge log</h3>
          <table class="audit-table">
            <thead>
              <tr>
                <th>Session</th>
                <th>Started</th>
                <th>Completed</th>
                <th>Status</th>
                <th>Merged</th>
                <th>Filtered</th>
                <th>Error</th>
              </tr>
            </thead>
            <tbody>
              {mergeRows.length === 0 && (
                <tr>
                  <td colSpan={7} class="empty">No merge log entries yet.</td>
                </tr>
              )}
              {mergeRows.map((r) => (
                <tr key={r.session_id}>
                  <td class="session-id">{r.session_id.substring(0, 12)}…</td>
                  <td>{formatDate(r.started_at)}</td>
                  <td>{formatDate(r.completed_at || 0)}</td>
                  <td>{r.status}</td>
                  <td>{r.rows_merged}</td>
                  <td>{r.rows_filtered}</td>
                  <td class="error-cell">{r.error || "-"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {activeTab === "filtered" && (
        <div class="audit-filtered">
          <h3>Filtered log — events excluded by privacy filter</h3>
          <p class="note">
            Only SHA-256 hashes are stored — never the raw payload content.
          </p>
          <table class="audit-table">
            <thead>
              <tr>
                <th>Session</th>
                <th>Time</th>
                <th>Event</th>
                <th>Rule</th>
                <th>Reason</th>
                <th>Hash</th>
              </tr>
            </thead>
            <tbody>
              {filterRows.length === 0 && (
                <tr>
                  <td colSpan={6} class="empty">No filtered events.</td>
                </tr>
              )}
              {filterRows.map((r, i) => (
                <tr key={i}>
                  <td class="session-id">{r.session_id.substring(0, 12)}…</td>
                  <td>{formatDate(r.ts)}</td>
                  <td>{r.event_type}</td>
                  <td>{r.filter_rule}</td>
                  <td>{r.excluded_reason}</td>
                  <td class="hash">{r.payload_hash.substring(0, 12)}…</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <button class="refresh-btn" onClick={loadData}>
        Refresh
      </button>
    </div>
  );
}
