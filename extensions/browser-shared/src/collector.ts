// TabCollector: wraps the browser tabs API (chrome.tabs / browser.tabs),
// tracks page active time, emits periodic tab-count events, and respects
// the user-configured domain blocklist.

import type { BrowserEvent, BrowserEventPayload, DomainCategory } from "./types";
import { extractDomain, isBlocked, redactEvent } from "./privacy";
import { classifyDomain } from "./classifier";

export type BrowserNamespace = "chrome" | "firefox";

export interface TabCollectorOptions {
  browser: BrowserNamespace;
  /** Called for each event ready to send. */
  onEvent: (event: BrowserEvent) => void;
  /** Returns the current blocklist from storage. */
  getBlocklist: () => Promise<string[]>;
}

interface ActiveTabState {
  tabId: number;
  domain: string;
  category: DomainCategory;
  title: string;
  activatedAt: number; // epoch ms
}

export class TabCollector {
  private readonly browser: BrowserNamespace;
  private readonly pluginName: string;
  private readonly onEvent: (event: BrowserEvent) => void;
  private readonly getBlocklist: () => Promise<string[]>;

  private activeTab: ActiveTabState | null = null;

  constructor(options: TabCollectorOptions) {
    this.browser = options.browser;
    this.pluginName = `${options.browser}-extension`;
    this.onEvent = options.onEvent;
    this.getBlocklist = options.getBlocklist;
  }

  /**
   * Start collecting. Call this from the service worker / background script.
   * Tab-count events are emitted by the background script via alarms, not here,
   * because the collector does not have direct access to the tabs query API.
   */
  start(): void {
    // Reserved for future periodic work within the collector.
  }

  /** Stop collecting and emit final page_time for the active tab. */
  stop(): void {
    this.emitPageTime();
    this.activeTab = null;
  }

  // --- Public handlers wired to browser event listeners ---

  /** Call when a tab is activated. */
  async handleTabActivated(tabId: number, url: string, title: string): Promise<void> {
    const previousDomain = this.activeTab?.domain;

    // Emit page_time for the previously active tab.
    this.emitPageTime();

    const domain = extractDomain(url);
    const blocklist = await this.getBlocklist();
    if (isBlocked(domain, blocklist)) {
      this.activeTab = null;
      return;
    }

    const category = classifyDomain(domain);
    this.activeTab = {
      tabId,
      domain,
      category,
      title,
      activatedAt: Date.now(),
    };

    this.emit({
      action: "tab_activated",
      browser: this.browser,
      tab_id: tabId,
      page_title: title,
      domain: domain || undefined,
      category,
      previous_domain: previousDomain || undefined,
    });
  }

  /** Call when a new tab is created. */
  async handleTabCreated(tabId: number, url: string, title: string): Promise<void> {
    const domain = extractDomain(url);
    const blocklist = await this.getBlocklist();
    if (isBlocked(domain, blocklist)) return;

    const category = classifyDomain(domain);
    this.emit({
      action: "tab_created",
      browser: this.browser,
      tab_id: tabId,
      page_title: title,
      domain: domain || undefined,
      category,
    });
  }

  /** Call when a tab is closed. */
  async handleTabRemoved(tabId: number): Promise<void> {
    // If the closed tab was the active one, emit page_time.
    if (this.activeTab && this.activeTab.tabId === tabId) {
      this.emitPageTime();
      this.activeTab = null;
    }

    this.emit({
      action: "tab_closed",
      browser: this.browser,
      tab_id: tabId,
    });
  }

  /** Call when a tab's URL changes (navigation). */
  async handleTabUpdated(
    tabId: number,
    url: string,
    title: string,
  ): Promise<void> {
    const domain = extractDomain(url);
    const blocklist = await this.getBlocklist();
    if (isBlocked(domain, blocklist)) {
      // If this was the active tab, clear it.
      if (this.activeTab && this.activeTab.tabId === tabId) {
        this.emitPageTime();
        this.activeTab = null;
      }
      return;
    }

    const category = classifyDomain(domain);

    this.emit({
      action: "tab_updated",
      browser: this.browser,
      tab_id: tabId,
      page_title: title,
      domain: domain || undefined,
      category,
    });

    // If this is the active tab and domain changed, reset page time tracking.
    if (this.activeTab && this.activeTab.tabId === tabId) {
      if (this.activeTab.domain !== domain) {
        this.emitPageTime();
        this.activeTab = {
          tabId,
          domain,
          category,
          title,
          activatedAt: Date.now(),
        };
      } else {
        // Same domain, just update title.
        this.activeTab.title = title;
      }
    }
  }

  // --- Private helpers ---

  private emitPageTime(): void {
    if (!this.activeTab) return;

    const activeSeconds = Math.round(
      (Date.now() - this.activeTab.activatedAt) / 1000,
    );
    if (activeSeconds < 1) return;

    this.emit({
      action: "page_time",
      browser: this.browser,
      tab_id: this.activeTab.tabId,
      page_title: this.activeTab.title,
      domain: this.activeTab.domain || undefined,
      category: this.activeTab.category,
      active_seconds: activeSeconds,
    });
  }

  private async emitTabCount(): Promise<void> {
    // Tab counting is done in the background script since it needs
    // access to chrome.tabs.query / browser.tabs.query.
    // The background script should call this method or emit directly.
    // We emit a placeholder — the background script overrides tab_count.
    this.emit({
      action: "tab_count",
      browser: this.browser,
      // tab_count will be set by the background script before sending.
    });
  }

  private emit(payload: BrowserEventPayload): void {
    const event: BrowserEvent = {
      plugin: this.pluginName,
      kind: "browser",
      timestamp: new Date().toISOString(),
      payload,
    };
    this.onEvent(redactEvent(event));
  }
}
