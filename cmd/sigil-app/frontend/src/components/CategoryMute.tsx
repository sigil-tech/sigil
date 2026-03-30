const CATEGORIES = [
  "workflow",
  "testing",
  "context-switch",
  "edit-pattern",
  "build",
  "stuck",
  "insight",
  "git",
];

export function CategoryMute({
  muted,
  onChange,
}: {
  muted: string[];
  onChange: (muted: string[]) => void;
}) {
  const toggle = (cat: string) => {
    if (muted.includes(cat)) {
      onChange(muted.filter((c) => c !== cat));
    } else {
      onChange([...muted, cat]);
    }
  };

  return (
    <div class="category-mute">
      {CATEGORIES.map((cat) => (
        <div key={cat} class="settings-row">
          <span class="settings-label">{cat}</span>
          <label
            class="toggle-row"
            style={{ justifyContent: "flex-end", width: "auto" }}
          >
            <input
              type="checkbox"
              checked={!muted.includes(cat)}
              onChange={() => toggle(cat)}
              style={{ display: "none" }}
            />
            <div class="toggle-switch">
              <div
                class={`toggle-track ${!muted.includes(cat) ? "active" : ""}`}
              >
                <div class="toggle-thumb" />
              </div>
            </div>
          </label>
        </div>
      ))}
    </div>
  );
}
