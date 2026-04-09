// SigilTransport: batches BrowserEvents and POSTs them to sigild's plugin
// ingest endpoint.  When sigild is unreachable, events are silently dropped.

import type { BrowserEvent } from "./types";

export interface TransportOptions {
  /** Ingest URL. Default: http://127.0.0.1:7775/api/v1/ingest */
  ingestURL?: string;
  /** Batch flush interval in ms. Default: 2000 */
  flushIntervalMs?: number;
  /** Connection health-check interval in ms. Default: 30000 */
  healthCheckIntervalMs?: number;
}

export type ConnectionStatus = "connected" | "disconnected" | "unknown";

export interface TransportStats {
  status: ConnectionStatus;
  eventsSent: number;
  eventsDropped: number;
  lastFlushTime: number | null; // epoch ms
}

export class SigilTransport {
  private readonly ingestURL: string;
  private readonly flushIntervalMs: number;
  private readonly healthCheckIntervalMs: number;

  private buffer: BrowserEvent[] = [];
  private flushTimer: ReturnType<typeof setInterval> | null = null;
  private healthTimer: ReturnType<typeof setInterval> | null = null;
  private _status: ConnectionStatus = "unknown";
  private _eventsSent = 0;
  private _eventsDropped = 0;
  private _lastFlushTime: number | null = null;

  /** Optional callback invoked whenever connection status changes. */
  onStatusChange?: (status: ConnectionStatus) => void;

  constructor(options: TransportOptions = {}) {
    this.ingestURL =
      options.ingestURL ?? "http://127.0.0.1:7775/api/v1/ingest";
    this.flushIntervalMs = options.flushIntervalMs ?? 2000;
    this.healthCheckIntervalMs = options.healthCheckIntervalMs ?? 30000;
  }

  /** Start the flush and health-check timers. */
  start(): void {
    if (this.flushTimer) return;
    this.flushTimer = setInterval(() => this.flush(), this.flushIntervalMs);
    this.healthTimer = setInterval(
      () => this.checkHealth(),
      this.healthCheckIntervalMs,
    );
    // Initial health check.
    this.checkHealth();
  }

  /** Stop timers and flush remaining events. */
  stop(): void {
    if (this.flushTimer) {
      clearInterval(this.flushTimer);
      this.flushTimer = null;
    }
    if (this.healthTimer) {
      clearInterval(this.healthTimer);
      this.healthTimer = null;
    }
    this.flush();
  }

  /** Enqueue an event for batched delivery. */
  send(event: BrowserEvent): void {
    this.buffer.push(event);
  }

  /** Get current transport statistics. */
  stats(): TransportStats {
    return {
      status: this._status,
      eventsSent: this._eventsSent,
      eventsDropped: this._eventsDropped,
      lastFlushTime: this._lastFlushTime,
    };
  }

  /** Flush the buffer immediately. */
  private async flush(): Promise<void> {
    if (this.buffer.length === 0) return;

    const batch = this.buffer.splice(0);

    try {
      const resp = await fetch(this.ingestURL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(batch),
        signal: AbortSignal.timeout(5000),
      });

      if (resp.ok) {
        this._eventsSent += batch.length;
        this._lastFlushTime = Date.now();
        this.setStatus("connected");
      } else {
        // Server returned an error — drop events silently.
        this._eventsDropped += batch.length;
        this.setStatus("disconnected");
      }
    } catch {
      // Network error (sigild not running) — drop events silently.
      this._eventsDropped += batch.length;
      this.setStatus("disconnected");
    }
  }

  /** Probe the sigild health endpoint. */
  private async checkHealth(): Promise<void> {
    const healthURL = this.ingestURL.replace(
      /\/api\/v1\/ingest$/,
      "/health",
    );
    try {
      const resp = await fetch(healthURL, {
        method: "GET",
        signal: AbortSignal.timeout(3000),
      });
      this.setStatus(resp.ok ? "connected" : "disconnected");
    } catch {
      this.setStatus("disconnected");
    }
  }

  private setStatus(status: ConnectionStatus): void {
    if (status !== this._status) {
      this._status = status;
      this.onStatusChange?.(status);
    }
  }
}
