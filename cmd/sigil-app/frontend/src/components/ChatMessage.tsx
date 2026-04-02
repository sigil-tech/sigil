import { CodeBlock } from "./CodeBlock";

interface Message {
  role: "user" | "assistant";
  content: string;
}

export function ChatMessage({ message }: { message: Message }) {
  const isUser = message.role === "user";

  return (
    <div class={`chat-message ${isUser ? "chat-user" : "chat-assistant"}`}>
      <div class="chat-bubble">
        {isUser ? message.content : renderContent(message.content)}
      </div>
    </div>
  );
}

// Parse content for code blocks (```lang\ncode\n```).
function renderContent(text: string) {
  const parts: any[] = [];
  const codeRegex = /```(\w*)\n([\s\S]*?)```/g;
  let lastIndex = 0;
  let match;

  while ((match = codeRegex.exec(text)) !== null) {
    // Text before code block.
    if (match.index > lastIndex) {
      parts.push(
        <span key={lastIndex}>{text.slice(lastIndex, match.index)}</span>
      );
    }
    // Code block.
    parts.push(
      <CodeBlock key={match.index} language={match[1]} code={match[2].trim()} />
    );
    lastIndex = match.index + match[0].length;
  }

  // Remaining text.
  if (lastIndex < text.length) {
    parts.push(<span key={lastIndex}>{text.slice(lastIndex)}</span>);
  }

  return parts.length > 0 ? parts : text;
}
