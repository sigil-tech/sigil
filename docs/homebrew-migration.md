# Homebrew Tap Migration Plan

## Current State

- **Tap repo**: `alecfeeman/homebrew-sigil` (personal account)
- **Source repo**: `sigil-tech/sigil` (org)
- **Workflow**: `.github/workflows/homebrew.yml` uses `mislav/bump-homebrew-formula-action@v3`
- **Status**: Failing with `401 Bad credentials` — the `HOMEBREW_TAP_TOKEN` secret is missing or expired

## What Needs to Happen

### Option A: Transfer tap to sigil-tech (recommended)

1. **Transfer the repo**
   - Alec: Go to `alecfeeman/homebrew-sigil` → Settings → Danger Zone → Transfer ownership → `sigil-tech`
   - GitHub automatically creates a redirect from the old URL

2. **Update the workflow**
   - Change `homebrew-tap` from `alecfeeman/homebrew-sigil` to `sigil-tech/homebrew-sigil`

3. **Create a PAT for the tap**
   - Create a fine-grained PAT scoped to `sigil-tech/homebrew-sigil` with `contents: write` permission
   - Recommended: use a bot account or org-level token rather than a personal one

4. **Add the secret**
   - Go to `sigil-tech/sigil` → Settings → Secrets → Actions
   - Create or update `HOMEBREW_TAP_TOKEN` with the new PAT

### Option B: Keep tap under alecfeeman

1. **Create a PAT**
   - Alec: Create a fine-grained PAT with `contents: write` on `alecfeeman/homebrew-sigil`
   - Classic PAT with `repo` scope also works but is less scoped

2. **Add the secret**
   - Nick or Alec (whoever has admin on `sigil-tech/sigil`): add the PAT as `HOMEBREW_TAP_TOKEN` in Actions secrets

3. **No workflow changes needed** — the tap reference is already `alecfeeman/homebrew-sigil`

## Additional Cleanup

### Update formula for v1 plugins

The homebrew formula should now install all v1 plugin binaries alongside `sigild` and `sigilctl`. The formula's `install` block should include:

```ruby
bin.install "sigild"
bin.install "sigilctl"
bin.install "sigil-plugin-claude"
bin.install "sigil-plugin-github"
bin.install "sigil-plugin-jira"
bin.install "sigil-plugin-vscode"
bin.install "sigil-plugin-jetbrains"
```

### Update sigil-ml formula references

The `sigild init` command references `alecfeeman/sigil/sigil-ml` as a brew formula. If the tap moves, update:
- `cmd/sigild/init_subcommand.go` line 385: `brew install alecfeeman/sigil/sigil-ml`
- `internal/plugin/registry.go`: any `BrewFormula` fields pointing to `alecfeeman/sigil/*`

### Verify the download URL

The workflow uses:
```
https://github.com/${{ github.repository }}/archive/refs/tags/${{ github.ref_name }}.tar.gz
```
This resolves to `sigil-tech/sigil` which is correct. No change needed.

## Testing the Fix

After completing either option:

1. Manually trigger the workflow: `gh workflow run homebrew.yml`
2. Or push a new tag (e.g. `v0.1.1-alpha.2`) and watch the release → homebrew chain
3. Verify: `brew tap <org>/sigil && brew install sigil`

## Who Does What

| Task | Owner | Blocked on |
|------|-------|------------|
| Decide Option A vs B | Nick + Alec | — |
| Transfer repo (Option A) | Alec | Decision |
| Create PAT | Alec (or bot account) | Decision |
| Add `HOMEBREW_TAP_TOKEN` secret | Nick (admin on sigil-tech/sigil) | PAT |
| Update workflow tap reference (Option A only) | Nick | Transfer |
| Update formula for v1 plugins | Nick | Working tap |
| Update `alecfeeman` references in source (Option A only) | Nick | Transfer |
