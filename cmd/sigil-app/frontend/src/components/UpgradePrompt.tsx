export function UpgradePrompt({
  feature,
  tier,
}: {
  feature: string;
  tier: string;
}) {
  return (
    <div class="upgrade-prompt">
      <div class="upgrade-prompt-text">
        <strong>{feature}</strong> requires the{" "}
        <span class="upgrade-tier">{tier}</span> tier.
      </div>
      <a
        class="btn btn-primary upgrade-btn"
        href="https://app.sigilos.io/billing"
        target="_blank"
        rel="noopener"
      >
        Upgrade
      </a>
    </div>
  );
}
