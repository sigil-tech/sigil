import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        RunInit(config: any): Promise<void>;
        DetectEnvironment(): Promise<{
          ides: string[];
          tools: string[];
          plugins: string[];
        }>;
      };
    };
  };
};

interface WizardConfig {
  watch_dirs: string[];
  inference_mode: string;
  notification_level: number;
  plugins: string[];
  cloud_enabled: boolean;
  cloud_provider: string;
  cloud_api_key: string;
  local_inference: boolean;
  fleet_enabled: boolean;
  fleet_endpoint: string;
}

const STEPS = [
  "Welcome",
  "Watch Dirs",
  "Inference",
  "Plugins",
  "Notifications",
  "Cloud",
  "Confirm",
] as const;

export function Wizard({ onComplete }: { onComplete: () => void }) {
  const [step, setStep] = useState(0);
  const [config, setConfig] = useState<WizardConfig>({
    watch_dirs: ["~/code"],
    inference_mode: "localfirst",
    notification_level: 2,
    plugins: [],
    cloud_enabled: false,
    cloud_provider: "",
    cloud_api_key: "",
    local_inference: true,
    fleet_enabled: false,
    fleet_endpoint: "",
  });
  const [detected, setDetected] = useState<{
    ides: string[];
    tools: string[];
    plugins: string[];
  } | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [newDir, setNewDir] = useState("");

  useEffect(() => {
    window.go.main.App.DetectEnvironment()
      .then(setDetected)
      .catch(() => {});
  }, []);

  const update = <K extends keyof WizardConfig>(
    key: K,
    value: WizardConfig[K]
  ) => {
    setConfig((prev) => ({ ...prev, [key]: value }));
  };

  const canNext = () => {
    if (step === 1) return config.watch_dirs.length > 0;
    return true;
  };

  const handleSubmit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      await window.go.main.App.RunInit(config);
      onComplete();
    } catch {
      setError("Failed to save configuration. Is the daemon running?");
    } finally {
      setSubmitting(false);
    }
  };

  const addDir = () => {
    const dir = newDir.trim();
    if (dir && !config.watch_dirs.includes(dir)) {
      update("watch_dirs", [...config.watch_dirs, dir]);
      setNewDir("");
    }
  };

  const removeDir = (dir: string) => {
    update(
      "watch_dirs",
      config.watch_dirs.filter((d) => d !== dir)
    );
  };

  const currentStep = STEPS[step];

  return (
    <div class="wizard">
      <div class="wizard-progress">
        {STEPS.map((s, i) => (
          <div
            key={s}
            class={`wizard-dot ${i === step ? "active" : ""} ${i < step ? "done" : ""}`}
          />
        ))}
      </div>

      <div class="wizard-content">
        {currentStep === "Welcome" && (
          <div class="wizard-step">
            <h2>Welcome to Sigil</h2>
            <p class="wizard-desc">
              Sigil observes your workflow — file edits, terminal commands, git
              activity — to detect patterns and surface suggestions that help you
              work faster.
            </p>
            <p class="wizard-desc">
              Everything stays on your machine. This wizard will configure the
              basics in about a minute.
            </p>
            {detected && detected.tools.length > 0 && (
              <div class="wizard-detected">
                <div class="wizard-detected-title">Detected on your system:</div>
                <div class="wizard-detected-list">
                  {detected.tools.map((t) => (
                    <span key={t} class="wizard-tag">
                      {t}
                    </span>
                  ))}
                  {detected.ides.map((ide) => (
                    <span key={ide} class="wizard-tag wizard-tag-accent">
                      {ide}
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {currentStep === "Watch Dirs" && (
          <div class="wizard-step">
            <h2>Watch Directories</h2>
            <p class="wizard-desc">
              Sigil watches these directories for file edits and discovers git
              repos within them.
            </p>
            <div class="wizard-dirs">
              {config.watch_dirs.map((dir) => (
                <div key={dir} class="wizard-dir-item">
                  <span>{dir}</span>
                  <button
                    class="wizard-dir-remove"
                    onClick={() => removeDir(dir)}
                  >
                    x
                  </button>
                </div>
              ))}
            </div>
            <div class="wizard-dir-add">
              <input
                type="text"
                class="settings-input"
                placeholder="~/projects"
                value={newDir}
                onInput={(e) => setNewDir((e.target as HTMLInputElement).value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    addDir();
                  }
                }}
              />
              <button class="btn" onClick={addDir}>
                Add
              </button>
            </div>
          </div>
        )}

        {currentStep === "Inference" && (
          <div class="wizard-step">
            <h2>AI Inference</h2>
            <p class="wizard-desc">
              Sigil can use AI to enrich its suggestions. Choose how inference
              should work.
            </p>
            <div class="wizard-options">
              <label class="wizard-radio">
                <input
                  type="radio"
                  name="inference"
                  checked={config.local_inference && !config.cloud_enabled}
                  onChange={() => {
                    update("local_inference", true);
                    update("cloud_enabled", false);
                    update("inference_mode", "local");
                  }}
                />
                <div>
                  <div class="wizard-radio-title">Local Only</div>
                  <div class="wizard-radio-desc">
                    All inference runs on your machine. Requires ~14GB for the
                    model. Most private.
                  </div>
                </div>
              </label>
              <label class="wizard-radio">
                <input
                  type="radio"
                  name="inference"
                  checked={config.local_inference && config.cloud_enabled}
                  onChange={() => {
                    update("local_inference", true);
                    update("cloud_enabled", true);
                    update("inference_mode", "localfirst");
                  }}
                />
                <div>
                  <div class="wizard-radio-title">
                    Hybrid (Local-first, Cloud fallback)
                  </div>
                  <div class="wizard-radio-desc">
                    Tries local first, falls back to cloud. Best balance of
                    privacy and quality.
                  </div>
                </div>
              </label>
              <label class="wizard-radio">
                <input
                  type="radio"
                  name="inference"
                  checked={!config.local_inference && config.cloud_enabled}
                  onChange={() => {
                    update("local_inference", false);
                    update("cloud_enabled", true);
                    update("inference_mode", "remote");
                  }}
                />
                <div>
                  <div class="wizard-radio-title">Cloud Only</div>
                  <div class="wizard-radio-desc">
                    Uses cloud AI for all inference. Faster, no local resources
                    needed. Requires API key.
                  </div>
                </div>
              </label>
              <label class="wizard-radio">
                <input
                  type="radio"
                  name="inference"
                  checked={!config.local_inference && !config.cloud_enabled}
                  onChange={() => {
                    update("local_inference", false);
                    update("cloud_enabled", false);
                    update("inference_mode", "localfirst");
                  }}
                />
                <div>
                  <div class="wizard-radio-title">Disabled</div>
                  <div class="wizard-radio-desc">
                    Heuristic-only suggestions. No AI enhancement. You can
                    enable this later.
                  </div>
                </div>
              </label>
            </div>
          </div>
        )}

        {currentStep === "Plugins" && (
          <div class="wizard-step">
            <h2>Plugins</h2>
            <p class="wizard-desc">
              Sigil plugins extend what the daemon can observe and act on.
            </p>
            {detected && detected.plugins.length > 0 ? (
              <div class="wizard-options">
                {detected.plugins.map((p) => (
                  <label key={p} class="wizard-checkbox">
                    <input
                      type="checkbox"
                      checked={config.plugins.includes(p)}
                      onChange={(e) => {
                        const checked = (e.target as HTMLInputElement).checked;
                        if (checked) {
                          update("plugins", [...config.plugins, p]);
                        } else {
                          update(
                            "plugins",
                            config.plugins.filter((x) => x !== p)
                          );
                        }
                      }}
                    />
                    <div>
                      <div class="wizard-radio-title">{p}</div>
                      <div class="wizard-radio-desc">
                        {p === "vscode"
                          ? "Surface suggestions as VS Code notifications"
                          : p === "jetbrains"
                            ? "Surface suggestions as JetBrains IDE notifications"
                            : `${p} plugin`}
                      </div>
                    </div>
                  </label>
                ))}
              </div>
            ) : (
              <div class="wizard-empty">
                No plugins detected. You can install plugins later from
                Settings.
              </div>
            )}
          </div>
        )}

        {currentStep === "Notifications" && (
          <div class="wizard-step">
            <h2>Notification Level</h2>
            <p class="wizard-desc">
              Control how aggressively Sigil surfaces suggestions.
            </p>
            <div class="wizard-options">
              {[
                {
                  level: 0,
                  name: "Silent",
                  desc: "No notifications. Check suggestions manually.",
                },
                {
                  level: 1,
                  name: "Digest",
                  desc: "Daily summary only, delivered once per day.",
                },
                {
                  level: 2,
                  name: "Ambient",
                  desc: "Passive suggestion bar. No interruptions. (Recommended)",
                },
                {
                  level: 3,
                  name: "Conversational",
                  desc: "Suggestions surfaced when you ask or at key moments.",
                },
                {
                  level: 4,
                  name: "Autonomous",
                  desc: "Auto-executes high-confidence actions with undo. Advanced.",
                },
              ].map((opt) => (
                <label key={opt.level} class="wizard-radio">
                  <input
                    type="radio"
                    name="notification_level"
                    checked={config.notification_level === opt.level}
                    onChange={() => update("notification_level", opt.level)}
                  />
                  <div>
                    <div class="wizard-radio-title">
                      {opt.level} — {opt.name}
                    </div>
                    <div class="wizard-radio-desc">{opt.desc}</div>
                  </div>
                </label>
              ))}
            </div>
          </div>
        )}

        {currentStep === "Cloud" && (
          <div class="wizard-step">
            <h2>Sigil Cloud</h2>
            <p class="wizard-desc">
              Optionally connect to Sigil Cloud for team insights and cloud AI.
              You can set this up later.
            </p>
            <div class="wizard-options">
              <label class="wizard-checkbox">
                <input
                  type="checkbox"
                  checked={config.fleet_enabled}
                  onChange={(e) =>
                    update(
                      "fleet_enabled",
                      (e.target as HTMLInputElement).checked
                    )
                  }
                />
                <div>
                  <div class="wizard-radio-title">Enable Team Insights</div>
                  <div class="wizard-radio-desc">
                    Share anonymized aggregate metrics with your team. No raw
                    data leaves your machine.
                  </div>
                </div>
              </label>
            </div>
            {config.cloud_enabled && (
              <div class="wizard-cloud-fields">
                <div class="settings-row">
                  <label class="settings-label">Cloud Provider</label>
                  <select
                    class="settings-select"
                    value={config.cloud_provider}
                    onChange={(e) =>
                      update(
                        "cloud_provider",
                        (e.target as HTMLSelectElement).value
                      )
                    }
                  >
                    <option value="">Select...</option>
                    <option value="anthropic">Anthropic</option>
                    <option value="openai">OpenAI</option>
                  </select>
                </div>
                <div class="settings-row">
                  <label class="settings-label">API Key</label>
                  <input
                    class="settings-input"
                    type="password"
                    value={config.cloud_api_key}
                    onInput={(e) =>
                      update(
                        "cloud_api_key",
                        (e.target as HTMLInputElement).value
                      )
                    }
                    placeholder="Enter API key..."
                  />
                </div>
              </div>
            )}
          </div>
        )}

        {currentStep === "Confirm" && (
          <div class="wizard-step">
            <h2>Ready to Go</h2>
            <p class="wizard-desc">Here's what Sigil will be configured with:</p>
            <div class="wizard-summary">
              <div class="wizard-summary-row">
                <span class="wizard-summary-label">Watch Dirs</span>
                <span>{config.watch_dirs.join(", ")}</span>
              </div>
              <div class="wizard-summary-row">
                <span class="wizard-summary-label">Inference</span>
                <span>{config.inference_mode}</span>
              </div>
              <div class="wizard-summary-row">
                <span class="wizard-summary-label">Notification Level</span>
                <span>{config.notification_level}</span>
              </div>
              <div class="wizard-summary-row">
                <span class="wizard-summary-label">Plugins</span>
                <span>
                  {config.plugins.length > 0
                    ? config.plugins.join(", ")
                    : "None"}
                </span>
              </div>
              <div class="wizard-summary-row">
                <span class="wizard-summary-label">Team Insights</span>
                <span>{config.fleet_enabled ? "Enabled" : "Disabled"}</span>
              </div>
            </div>
            {error && <div class="wizard-error">{error}</div>}
          </div>
        )}
      </div>

      <div class="wizard-nav">
        {step > 0 && (
          <button class="btn" onClick={() => setStep(step - 1)}>
            Back
          </button>
        )}
        <div class="wizard-nav-spacer" />
        {step < STEPS.length - 1 ? (
          <button
            class="btn btn-primary"
            onClick={() => setStep(step + 1)}
            disabled={!canNext()}
          >
            Next
          </button>
        ) : (
          <button
            class="btn btn-primary"
            onClick={handleSubmit}
            disabled={submitting}
          >
            {submitting ? "Starting Sigil..." : "Start Sigil"}
          </button>
        )}
      </div>
    </div>
  );
}
