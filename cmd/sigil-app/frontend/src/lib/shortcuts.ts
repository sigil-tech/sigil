// Keyboard shortcut manager for Sigil desktop app.
// Uses Meta (Cmd on macOS) or Control (Linux/Windows) as the modifier.

type NavigateFn = (view: string) => void;
type FocusFn = (id: string) => void;

const isMac = typeof navigator !== "undefined" && /Mac/.test(navigator.platform);

export function registerShortcuts(
  navigate: NavigateFn,
  focus: FocusFn
): () => void {
  const handler = (e: KeyboardEvent) => {
    const mod = isMac ? e.metaKey : e.ctrlKey;
    if (!mod) return;

    switch (e.key) {
      case "1":
        e.preventDefault();
        navigate("list");
        break;
      case "2":
        e.preventDefault();
        navigate("summary");
        break;
      case "3":
        e.preventDefault();
        navigate("ask");
        break;
      case "4":
        e.preventDefault();
        navigate("plugins");
        break;
      case "5":
        e.preventDefault();
        navigate("analytics");
        break;
      case "6":
        e.preventDefault();
        navigate("settings");
        break;
      case ",":
        e.preventDefault();
        navigate("settings");
        break;
      case "k":
        e.preventDefault();
        navigate("ask");
        // Focus the ask input after navigation.
        requestAnimationFrame(() => focus("ask-input"));
        break;
      case "f":
        e.preventDefault();
        focus("search-input");
        break;
    }
  };

  document.addEventListener("keydown", handler);
  return () => document.removeEventListener("keydown", handler);
}

// Focus a specific element by class name.
export function focusElement(id: string) {
  const el = document.querySelector(`.${id}`) as HTMLElement | null;
  if (el) el.focus();
}
