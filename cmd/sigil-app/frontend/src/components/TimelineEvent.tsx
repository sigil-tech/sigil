import { useState } from "preact/hooks";

interface TimelineEventData {
  timestamp: string;
  kind: string;
  summary: string;
  detail?: Record<string, any>;
}

const KIND_ICONS: Record<string, string> = {
  file: "\u{1F4C4}",      // document
  git: "\u{1F500}",       // branch
  process: "\u{1F4BB}",   // terminal
  terminal: "\u{1F4BB}",  // terminal
  task: "\u{1F3F4}",      // flag
  suggestion: "\u{1F4A1}", // lightbulb
  ai: "\u{1F916}",        // robot
  hyprland: "\u{1F5A5}",  // monitor
  clipboard: "\u{1F4CB}", // clipboard
};

export function TimelineEventItem({ event }: { event: TimelineEventData }) {
  const [expanded, setExpanded] = useState(false);

  const time = new Date(event.timestamp);
  const timeStr = time.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });

  const icon = KIND_ICONS[event.kind] || "\u{1F4CC}"; // pin fallback

  return (
    <div class="timeline-event" onClick={() => setExpanded(!expanded)}>
      <div class="timeline-event-row">
        <span class="timeline-icon">{icon}</span>
        <span class="timeline-time">{timeStr}</span>
        <span class="timeline-summary">{event.summary}</span>
        <span class="timeline-kind">{event.kind}</span>
      </div>
      {expanded && event.detail && (
        <pre class="timeline-detail">
          {JSON.stringify(event.detail, null, 2)}
        </pre>
      )}
    </div>
  );
}
