import { useState } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        DownloadUpdate(url: string, checksum: string): Promise<void>;
        ApplyUpdate(): Promise<void>;
      };
    };
  };
};

export interface UpdateInfoPayload {
  version: string;
  changelog: string;
  url: string;
  checksum: string;
}

interface UpdateBannerProps {
  info: UpdateInfoPayload;
  onDismiss: () => void;
}

type Phase = "available" | "downloading" | "downloaded" | "applying" | "applied" | "error";

export function UpdateBanner({ info, onDismiss }: UpdateBannerProps) {
  const [phase, setPhase] = useState<Phase>("available");
  const [error, setError] = useState<string | null>(null);

  const changelogLines = (info.changelog || "")
    .split("\n")
    .filter((l) => l.trim() !== "")
    .slice(0, 3);

  const handleDismiss = () => {
    try {
      localStorage.setItem(
        "sigil_update_snoozed",
        String(Date.now() + 24 * 60 * 60 * 1000)
      );
    } catch {
      // localStorage unavailable.
    }
    onDismiss();
  };

  const handleDownload = async () => {
    setPhase("downloading");
    setError(null);
    try {
      await window.go.main.App.DownloadUpdate(info.url, info.checksum);
      setPhase("downloaded");
    } catch (e: any) {
      setError(e?.message || "Download failed");
      setPhase("error");
    }
  };

  const handleApply = async () => {
    setPhase("applying");
    setError(null);
    try {
      await window.go.main.App.ApplyUpdate();
      setPhase("applied");
    } catch (e: any) {
      setError(e?.message || "Apply failed");
      setPhase("error");
    }
  };

  return (
    <div class="update-banner">
      <div class="update-banner-content">
        <div class="update-banner-title">
          {phase === "applied"
            ? "Update installed"
            : `Update available: v${info.version}`}
        </div>

        {changelogLines.length > 0 && phase !== "applied" && (
          <div class="update-banner-changelog">
            {changelogLines.map((line, i) => (
              <div key={i} class="update-changelog-line">
                {line}
              </div>
            ))}
          </div>
        )}

        {error && <div class="update-banner-error">{error}</div>}

        {phase === "applied" && (
          <div class="update-banner-restart">
            Restart the app to complete the update.
          </div>
        )}

        {phase === "downloading" && (
          <div class="update-banner-progress">
            <div class="update-progress-bar">
              <div class="update-progress-indeterminate" />
            </div>
            <span class="update-progress-text">Downloading...</span>
          </div>
        )}

        {phase === "applying" && (
          <div class="update-banner-progress">
            <div class="update-progress-bar">
              <div class="update-progress-indeterminate" />
            </div>
            <span class="update-progress-text">Applying...</span>
          </div>
        )}
      </div>

      <div class="update-banner-actions">
        {phase === "available" && (
          <>
            <button class="btn btn-primary update-btn" onClick={handleDownload}>
              Update Now
            </button>
            <button class="btn btn-dismiss update-btn-dismiss" onClick={handleDismiss}>
              Dismiss
            </button>
          </>
        )}
        {phase === "downloaded" && (
          <button class="btn btn-primary update-btn" onClick={handleApply}>
            Install & Restart
          </button>
        )}
        {phase === "error" && (
          <button class="btn btn-primary update-btn" onClick={handleDownload}>
            Retry
          </button>
        )}
      </div>
    </div>
  );
}
