interface ToggleProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label?: string;
}

export function Toggle({ checked, onChange, label }: ToggleProps) {
  return (
    <label class="toggle-row">
      {label && <span class="toggle-label">{label}</span>}
      <div class="toggle-switch" onClick={() => onChange(!checked)}>
        <div class={`toggle-track ${checked ? 'active' : ''}`}>
          <div class="toggle-thumb" />
        </div>
      </div>
    </label>
  );
}
