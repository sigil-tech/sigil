// Popup logic — reads stats from chrome.storage.local, manages blocklist.

function $(id: string): HTMLElement {
  return document.getElementById(id)!;
}

// --- Status & stats ---

async function refreshStatus(): Promise<void> {
  const data = await chrome.storage.local.get([
    "connectionStatus",
    "eventsSent",
    "eventsDropped",
  ]);

  const status = (data.connectionStatus as string) ?? "unknown";
  const dot = $("status-dot");
  dot.className = `status-dot ${status}`;

  const labels: Record<string, string> = {
    connected: "Connected to sigild",
    disconnected: "Disconnected",
    unknown: "Checking...",
  };
  $("status-text").textContent = labels[status] ?? status;
  $("events-sent").textContent = String(data.eventsSent ?? 0);
  $("events-dropped").textContent = String(data.eventsDropped ?? 0);
}

// --- Blocklist management ---

async function getBlocklist(): Promise<string[]> {
  const result = await chrome.storage.local.get("blocklist");
  return (result.blocklist as string[]) ?? [];
}

async function saveBlocklist(list: string[]): Promise<void> {
  await chrome.storage.local.set({ blocklist: list });
}

function renderBlocklist(list: string[]): void {
  const container = $("blocklist-items");
  if (list.length === 0) {
    container.innerHTML = '<div class="empty-msg">No blocked domains</div>';
    return;
  }

  container.innerHTML = "";
  for (const domain of list) {
    const item = document.createElement("div");
    item.className = "blocklist-item";

    const label = document.createElement("span");
    label.textContent = domain;

    const removeBtn = document.createElement("button");
    removeBtn.textContent = "\u00d7"; // multiplication sign (x)
    removeBtn.title = "Remove";
    removeBtn.addEventListener("click", async () => {
      const current = await getBlocklist();
      const updated = current.filter((d) => d !== domain);
      await saveBlocklist(updated);
      renderBlocklist(updated);
    });

    item.appendChild(label);
    item.appendChild(removeBtn);
    container.appendChild(item);
  }
}

async function addDomain(): Promise<void> {
  const input = $("domain-input") as HTMLInputElement;
  const domain = input.value.trim().toLowerCase();
  if (!domain) return;

  const current = await getBlocklist();
  if (!current.includes(domain)) {
    current.push(domain);
    await saveBlocklist(current);
  }

  input.value = "";
  renderBlocklist(current);
}

// --- Init ---

document.addEventListener("DOMContentLoaded", async () => {
  await refreshStatus();

  const blocklist = await getBlocklist();
  renderBlocklist(blocklist);

  $("add-btn").addEventListener("click", addDomain);
  ($("domain-input") as HTMLInputElement).addEventListener("keydown", (e) => {
    if (e.key === "Enter") addDomain();
  });

  // Refresh status while popup is open.
  setInterval(refreshStatus, 2000);
});
