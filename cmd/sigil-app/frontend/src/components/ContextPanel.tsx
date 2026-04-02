import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetCurrentTask(): Promise<any>;
        GetStatus(): Promise<any>;
      };
    };
  };
};

export function ContextPanel() {
  const [task, setTask] = useState<any>(null);
  const [collapsed, setCollapsed] = useState(false);

  useEffect(() => {
    const fetchContext = () => {
      window.go.main.App.GetCurrentTask().then(setTask).catch(() => {});
    };
    fetchContext();

    const onFocus = () => fetchContext();
    globalThis.addEventListener("focus", onFocus);
    return () => globalThis.removeEventListener("focus", onFocus);
  }, []);

  if (!task || !task.id) return null;

  return (
    <div class={`context-panel ${collapsed ? "collapsed" : ""}`}>
      <button
        class="context-panel-toggle"
        onClick={() => setCollapsed(!collapsed)}
      >
        Context {collapsed ? "+" : "-"}
      </button>
      {!collapsed && (
        <div class="context-panel-body">
          {task.branch && (
            <div class="context-item">
              <span class="context-label">Branch</span>
              <span class="context-value">{task.branch}</span>
            </div>
          )}
          {task.description && (
            <div class="context-item">
              <span class="context-label">Task</span>
              <span class="context-value">{task.description}</span>
            </div>
          )}
          {task.files && task.files.length > 0 && (
            <div class="context-item">
              <span class="context-label">Files</span>
              <span class="context-value">
                {task.files.slice(0, 5).map((f: string) => (
                  <div key={f} class="context-file">{f.split("/").pop()}</div>
                ))}
              </span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
