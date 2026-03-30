import { useState, useRef, useEffect } from "preact/hooks";
import { ChatMessage } from "../components/ChatMessage";
import { ContextPanel } from "../components/ContextPanel";

declare const window: Window & {
  go: {
    main: {
      App: {
        AskWithContext(
          query: string,
          ctx: { task?: string; branch?: string; recent_files?: string[] }
        ): Promise<any>;
        GetCurrentTask(): Promise<any>;
      };
    };
  };
};

interface Message {
  role: "user" | "assistant";
  content: string;
  timestamp: Date;
}

export function AskSigil() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // Scroll to bottom on new messages.
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages]);

  const handleSubmit = async () => {
    const q = query.trim();
    if (!q || loading) return;

    const userMsg: Message = { role: "user", content: q, timestamp: new Date() };
    setMessages((prev) => [...prev, userMsg]);
    setQuery("");
    setLoading(true);

    try {
      // Fetch current task context.
      let ctx: { task?: string; branch?: string } = {};
      try {
        const task = await window.go.main.App.GetCurrentTask();
        if (task) {
          ctx.task = task.description || task.id;
          ctx.branch = task.branch;
        }
      } catch {
        // Context unavailable — send query without it.
      }

      const result = await window.go.main.App.AskWithContext(q, ctx);
      const answer =
        result?.answer || result?.response || JSON.stringify(result, null, 2);

      setMessages((prev) => [
        ...prev,
        { role: "assistant", content: answer, timestamp: new Date() },
      ]);
    } catch {
      setMessages((prev) => [
        ...prev,
        {
          role: "assistant",
          content: "Could not reach the daemon. Is it running?",
          timestamp: new Date(),
        },
      ]);
    } finally {
      setLoading(false);
    }
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const handleClear = () => {
    setMessages([]);
  };

  return (
    <div class="ask-view ask-chat">
      <ContextPanel />

      <div class="chat-messages" ref={scrollRef}>
        {messages.length === 0 && (
          <div class="empty-state">
            <div class="empty-state-title">Ask Sigil</div>
            <div class="empty-state-text">
              Ask questions about your workflow, codebase, or patterns Sigil has
              observed. Conversations persist until you clear them.
            </div>
          </div>
        )}
        {messages.map((msg, i) => (
          <ChatMessage key={i} message={msg} />
        ))}
        {loading && (
          <div class="chat-message chat-assistant">
            <div class="chat-bubble">
              <span class="loading-spinner" />
            </div>
          </div>
        )}
      </div>

      <div class="chat-input-area">
        {messages.length > 0 && (
          <button class="btn chat-clear" onClick={handleClear}>
            Clear
          </button>
        )}
        <input
          ref={inputRef}
          class="ask-input"
          type="text"
          placeholder="Ask Sigil anything..."
          value={query}
          onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
          onKeyDown={handleKeyDown}
        />
        <button
          class="btn btn-primary"
          onClick={handleSubmit}
          disabled={loading || !query.trim()}
        >
          {loading ? <span class="loading-spinner" /> : "Send"}
        </button>
      </div>
    </div>
  );
}
