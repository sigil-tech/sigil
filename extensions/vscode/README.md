# Sigil for VS Code

Surfaces [sigild](../../) suggestions directly in VS Code as notification toasts.

## Install

Build the `.vsix` and install it:

```bash
cd extensions/vscode
npm install
npm run compile
npm run package
code --install-extension sigil-vscode-0.1.0.vsix
```

## Usage

1. Start `sigild` (the Sigil daemon)
2. Open VS Code — the extension activates automatically
3. Look for "Sigil: Connected" in the status bar
4. Suggestions appear as notification toasts with **Accept** / **Dismiss** buttons
5. Use the command palette: **Sigil: Show Suggestions** to browse history

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `sigil.socketPath` | Auto-detected | Path to the sigild Unix socket |

The socket path is auto-detected from `$XDG_RUNTIME_DIR/sigild.sock`. Override via VS Code settings if your daemon uses a non-standard path.

## How it works

The extension connects to the `sigild` Unix socket and subscribes to real-time suggestion push events. When connected, desktop notifications (`notify-send`) are automatically suppressed — only one notification channel is active at a time.

Feedback (accept/dismiss) is sent back to the daemon via the socket API, so your suggestion acceptance patterns inform future recommendations.
