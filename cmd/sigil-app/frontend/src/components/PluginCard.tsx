interface PluginCardProps {
  name: string;
  description: string;
  version?: string;
  category?: string;
  installed: boolean;
  enabled?: boolean;
  running?: boolean;
  healthy?: boolean;
  onToggle?: (enabled: boolean) => void;
  onInstall?: () => void;
  installing?: boolean;
}

export function PluginCard(props: PluginCardProps) {
  const { name, description, version, category, installed, enabled, running, healthy, onToggle, onInstall, installing } = props;

  return (
    <div class="plugin-card">
      <div class="plugin-card-header">
        <div class="plugin-card-title">
          <span class="plugin-name">{name}</span>
          {version && <span class="version-badge">{version}</span>}
          {category && <span class={`category-badge category-${category}`}>{category}</span>}
        </div>
        <div class="plugin-card-actions">
          {installed && onToggle && (
            <div class="toggle-switch" onClick={() => onToggle(!enabled)}>
              <div class={`toggle-track ${enabled ? 'active' : ''}`}>
                <div class="toggle-thumb" />
              </div>
            </div>
          )}
          {!installed && onInstall && (
            <button class="install-btn" onClick={onInstall} disabled={installing}>
              {installing ? 'Installing...' : 'Install'}
            </button>
          )}
        </div>
      </div>
      <div class="plugin-card-body">
        <p class="plugin-description">{description}</p>
        {installed && (
          <div class="plugin-status-row">
            <span class={`status-dot ${running ? 'running' : 'stopped'}`} />
            <span>{running ? 'Running' : 'Stopped'}</span>
            {running && (
              <>
                <span class={`status-dot ${healthy ? 'healthy' : 'unhealthy'}`} />
                <span>{healthy ? 'Healthy' : 'Unhealthy'}</span>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
