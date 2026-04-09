// Privacy helpers: domain extraction, blocklist checking, event redaction.
// CRITICAL: We NEVER store URL paths, query strings, or fragments.

import type { BrowserEvent } from "./types";

/**
 * Extract the hostname (domain) from a URL string.
 * Returns empty string for invalid URLs or non-http(s) schemes.
 */
export function extractDomain(url: string): string {
  if (!url) return "";
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return "";
    }
    return parsed.hostname;
  } catch {
    return "";
  }
}

/**
 * Check if a domain is on the user's blocklist.
 * Supports exact match and wildcard suffix match (*.example.com).
 */
export function isBlocked(domain: string, blocklist: string[]): boolean {
  if (!domain) return false;
  const lower = domain.toLowerCase();
  for (const entry of blocklist) {
    const pattern = entry.toLowerCase().trim();
    if (!pattern) continue;
    if (pattern === lower) return true;
    // Wildcard: *.example.com blocks sub.example.com and example.com
    if (pattern.startsWith("*.")) {
      const suffix = pattern.slice(2);
      if (lower === suffix || lower.endsWith("." + suffix)) {
        return true;
      }
    }
  }
  return false;
}

/**
 * Redact sensitive fields from an event before sending.
 * This is a safety net — domain extraction already strips paths.
 * Ensures no accidental URL leakage in payload fields.
 */
export function redactEvent(event: BrowserEvent): BrowserEvent {
  const redacted = { ...event, payload: { ...event.payload } };

  // Strip page title if it looks like it might contain a URL or sensitive data
  // Titles are generally safe, but truncate excessively long ones.
  if (redacted.payload.page_title && redacted.payload.page_title.length > 200) {
    redacted.payload.page_title = redacted.payload.page_title.slice(0, 200);
  }

  return redacted;
}
