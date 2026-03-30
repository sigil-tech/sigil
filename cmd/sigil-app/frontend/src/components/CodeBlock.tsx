import { useState } from "preact/hooks";

export function CodeBlock({ code, language }: { code: string; language: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard API may not be available.
    }
  };

  return (
    <div class="code-block">
      <div class="code-block-header">
        <span class="code-block-lang">{language || "text"}</span>
        <button class="code-block-copy" onClick={handleCopy}>
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre class="code-block-content">
        <code>{code}</code>
      </pre>
    </div>
  );
}
