import { useState, useEffect } from "preact/hooks";
import { TimelineEventItem } from "../components/TimelineEvent";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetTimeline(
          date: string,
          types: string[],
          offset: number,
          limit: number
        ): Promise<{ events: any[]; total: number }>;
      };
    };
  };
};

const EVENT_TYPES = ["file", "git", "terminal", "process", "hyprland", "ai", "clipboard"];
const PAGE_SIZE = 50;

export function Timeline() {
  const [events, setEvents] = useState<any[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [activeTypes, setActiveTypes] = useState<Set<string>>(new Set(EVENT_TYPES));
  const [offset, setOffset] = useState(0);

  const fetchEvents = (off: number, types: Set<string>) => {
    setLoading(true);
    const today = new Date().toISOString().split("T")[0];
    window.go.main.App.GetTimeline(today, Array.from(types), off, PAGE_SIZE)
      .then((result) => {
        if (off === 0) {
          setEvents(result.events || []);
        } else {
          setEvents((prev) => [...prev, ...(result.events || [])]);
        }
        setTotal(result.total || 0);
        setError(null);
      })
      .catch(() => setError("Could not fetch timeline. Is the daemon running?"))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    setOffset(0);
    fetchEvents(0, activeTypes);
  }, [activeTypes]);

  const toggleType = (type: string) => {
    setActiveTypes((prev) => {
      const next = new Set(prev);
      if (next.has(type)) {
        next.delete(type);
      } else {
        next.add(type);
      }
      return next;
    });
  };

  const loadMore = () => {
    const newOffset = offset + PAGE_SIZE;
    setOffset(newOffset);
    fetchEvents(newOffset, activeTypes);
  };

  if (error && events.length === 0) {
    return (
      <div class="timeline-view">
        <div class="empty-state">
          <div class="empty-state-title">Unavailable</div>
          <div class="empty-state-text">{error}</div>
        </div>
      </div>
    );
  }

  return (
    <div class="timeline-view">
      {/* Filter bar */}
      <div class="filter-bar">
        {EVENT_TYPES.map((type) => (
          <button
            key={type}
            class={`filter-btn ${activeTypes.has(type) ? "active" : ""}`}
            onClick={() => toggleType(type)}
          >
            {type}
          </button>
        ))}
      </div>

      {/* Events */}
      <div class="timeline-list">
        {events.length === 0 && !loading && (
          <div class="empty-state">
            <div class="empty-state-title">No events today</div>
            <div class="empty-state-text">Start working and events will appear here.</div>
          </div>
        )}
        {events.map((evt, i) => (
          <TimelineEventItem key={i} event={evt} />
        ))}
      </div>

      {/* Load more */}
      {events.length < total && (
        <div class="timeline-load-more">
          <button class="btn" onClick={loadMore} disabled={loading}>
            {loading ? "Loading..." : `Load more (${total - events.length} remaining)`}
          </button>
        </div>
      )}

      {loading && events.length === 0 && (
        <div class="empty-state">
          <div class="loading-spinner" />
        </div>
      )}
    </div>
  );
}
