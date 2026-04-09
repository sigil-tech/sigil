// Firefox extension background script: wires TabCollector + SigilTransport.
// Uses browser.* namespace (WebExtension API) instead of chrome.*.

import { SigilTransport } from "../../browser-shared/src/transport";
import { TabCollector } from "../../browser-shared/src/collector";
import type { BrowserEvent } from "../../browser-shared/src/types";

// --- Globals ---

const transport = new SigilTransport();

const collector = new TabCollector({
  browser: "firefox",
  onEvent: (event: BrowserEvent) => transport.send(event),
  getBlocklist,
});

// --- Blocklist helpers ---

async function getBlocklist(): Promise<string[]> {
  const result = await browser.storage.local.get("blocklist");
  return (result.blocklist as string[]) ?? [];
}

// --- Tab event wiring ---

browser.tabs.onActivated.addListener(async (activeInfo) => {
  try {
    const tab = await browser.tabs.get(activeInfo.tabId);
    await collector.handleTabActivated(
      activeInfo.tabId,
      tab.url ?? "",
      tab.title ?? "",
    );
  } catch {
    // Tab may have been closed before we could query it.
  }
});

browser.tabs.onCreated.addListener(async (tab) => {
  await collector.handleTabCreated(tab.id ?? 0, tab.url ?? "", tab.title ?? "");
});

browser.tabs.onRemoved.addListener(async (tabId) => {
  await collector.handleTabRemoved(tabId);
});

browser.tabs.onUpdated.addListener(async (tabId, changeInfo, tab) => {
  // Only fire on complete navigation to avoid duplicate events.
  if (changeInfo.status !== "complete") return;
  await collector.handleTabUpdated(tabId, tab.url ?? "", tab.title ?? "");
});

// --- Periodic tab count ---

const TAB_COUNT_INTERVAL_MS = 60_000;

async function emitTabCount(): Promise<void> {
  try {
    const tabs = await browser.tabs.query({});
    const event: BrowserEvent = {
      plugin: "firefox-extension",
      kind: "browser",
      timestamp: new Date().toISOString(),
      payload: {
        action: "tab_count",
        browser: "firefox",
        tab_count: tabs.length,
      },
    };
    transport.send(event);
  } catch {
    // Query may fail during shutdown.
  }
}

// Use browser.alarms for periodic work (survives background script restarts).
browser.alarms.create("sigil-tab-count", {
  periodInMinutes: TAB_COUNT_INTERVAL_MS / 60_000,
});

browser.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "sigil-tab-count") {
    emitTabCount();
  }
});

// --- Connection status forwarding to popup ---

transport.onStatusChange = (status) => {
  browser.storage.local.set({ connectionStatus: status });
};

// Persist stats periodically so popup can read them.
setInterval(() => {
  const stats = transport.stats();
  browser.storage.local.set({
    connectionStatus: stats.status,
    eventsSent: stats.eventsSent,
    eventsDropped: stats.eventsDropped,
  });
}, 5000);

// --- Start ---

transport.start();
collector.start();

// Initial tab count on startup.
emitTabCount();
