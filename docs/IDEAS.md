
This is a really compelling concept, Nick. Nothing exists that matches this full vision, but there are pieces in the ecosystem worth knowing about, and some projects that rhyme with parts of it.

What exists in adjacent spaces:

The closest spiritual ancestors are probably custom Linux spins aimed at developers — things like Fedora Sway Spin or EndeavourOS with a tiling WM — which give you a minimal, keyboard-driven environment with a terminal at the center. But none of them have the AI-native layer you're describing.

On the AI-OS side, there have been consumer-oriented attempts like Rabbit OS and Humane's Cosmos, but those went the opposite direction — abstracting away from the developer and toward a general consumer intent model. They also largely flopped.

The pieces that exist today but aren't unified:

    Warp / Ghostty / Kitty for AI-augmented or high-performance terminals
    Cursor / Windsurf / Claude Code for AI-native development
    LiteLLM / OpenRouter for model routing
    NixOS for a declarative, reproducible, minimal base OS that you could strip down to exactly what you described
    Tauri or Electron shells for wrapping a minimal Chromium browser

The self-tuning layer is the part that truly doesn't exist yet. The idea of an OS-level daemon that observes your workflows, collects telemetry about how you develop (not just what you develop), and uses that to reshape the environment — adjusting keybindings, surfacing relevant docs, pre-warming contexts, tuning resource allocation — that's genuinely novel as a product.

Why it probably hasn't been built:

The hard part isn't any individual component; it's that the value proposition requires the whole stack to be integrated. A minimal tiling WM + embedded terminal + IDE + LLM router is a weekend project on NixOS. But the self-tuning feedback loop requires deep OS-level instrumentation, a thoughtful privacy model (especially for a developer audience that's allergic to telemetry), and a training/fine-tuning pipeline that actually improves the experience over time without becoming annoying or presumptuous.

There's also a chicken-and-egg problem: you need a critical mass of users to generate enough signal for the AI layer to be useful, but you need the AI layer to be useful to attract users away from their current setups.

That said — if I were designing this, NixOS as the base with a Wayland compositor (something like Hyprland), an embedded Warp-like terminal, Cursor or a Theia-based IDE, and a local LLM agent daemon would get you 80% of the way there as a proof of concept. The remaining 20% — the adaptive feedback loop — is where the real IP would live. Given your Golang and infrastructure background, the daemon/agent layer is probably the most natural place for you to start prototyping if you wanted to explore this seriously.

What a daemon actually is:

A daemon is just a long-running background process with no terminal attached. On Linux, you've interacted with them constantly — systemd, dockerd, kubelet — they're all daemons. The "mystique" around writing one is overblown. In Go, your main() function just... never returns. It sits in a loop, listens on sockets, watches for signals, and does work.

Architecture for this specific agent daemon:

Think of it as three subsystems:

1. The Collector (observation layer)

This is the part that watches what you do. It would hook into OS-level event sources to build a picture of your workflow patterns. Things like: which files you touch most, your terminal command frequency and patterns, window focus duration, git activity cadence, what times of day you're most productive, which docs/URLs you visit repeatedly, build/test failure patterns.

On Linux, the building blocks are inotify (file system events), reading from /proc (process info), D-Bus for desktop events, and potentially eBPF if you want deeper kernel-level instrumentation without modifying the kernel. In Go, libraries like fsnotify wrap inotify nicely. For X11/Wayland window events, you'd interface with the compositor's IPC — Hyprland has a nice Unix socket-based IPC, for example.

The collector writes structured events to a local store. SQLite is honestly fine for a prototype. You'd end up with a time-series of "user did X at time T in context C."

2. The Analyzer (intelligence layer)

This consumes the event stream and does two things: builds a local user model (statistical/heuristic) and periodically calls out to an LLM for higher-level reasoning. The local model handles things like "Nick usually runs tests after editing files in /pkg/decisioning" or "Nick's build failures spike on Mondays." These are just frequency tables and simple pattern detection — no ML required.

The LLM callout is for the more interesting stuff: "Based on the last week of activity, Nick seems to be stuck on integrating service X with service Y. Here are three things that might help." You'd batch up a summarized context window and send it through your LiteLLM router. This is where the ANTHROPIC_API_KEY or whatever model provider you're using comes in.

3. The Actuator (action layer)

This is where the daemon actually changes the environment. It could range from passive (desktop notifications, a dashboard widget) to active (reconfiguring your IDE, adjusting system resources, pre-opening relevant files). Start passive. The notification path on Linux is straightforward — just send D-Bus notifications. Over time, you'd build deeper integrations: talking to your IDE's LSP, adjusting your compositor layout, pre-warming Docker containers you're likely to need.

Getting started concretely:

Here's what I'd do in your shoes, as a first weekend project:

Start with a single Go binary that runs as a systemd service. It does one thing: watches your active terminal and git repos using fsnotify and a lightweight polling loop, logs events to SQLite, and once per hour summarizes the activity and sends it to Claude via the API asking "what patterns do you notice and what might help this developer?" Display the response as a desktop notification.

That's your v0. It's maybe 500 lines of Go. From there, you iterate on what data you collect, what you ask the LLM, and what actions you take.
One architectural note — given your thinking about privacy and the telemetry-averse developer audience, I'd build the local user model as a first-class concept from day one. All raw event data stays local. Only summarized, anonymized context goes to the LLM. You could even run a local model (Ollama + something small like Phi-3) for the routine analysis and only call out to frontier models for the deeper reasoning. That's a strong privacy story.