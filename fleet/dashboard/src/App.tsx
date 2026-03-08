import { useState } from "preact/hooks";
import { AdoptionView } from "./views/AdoptionView";
import { VelocityView } from "./views/VelocityView";

type View = "adoption" | "velocity";

export function App() {
  const [view, setView] = useState<View>("adoption");

  return (
    <div>
      <header>
        <h1>Aether Fleet Dashboard</h1>
        <nav>
          <button onClick={() => setView("adoption")} class={view === "adoption" ? "active" : ""}>
            Adoption
          </button>
          <button onClick={() => setView("velocity")} class={view === "velocity" ? "active" : ""}>
            Velocity
          </button>
        </nav>
      </header>
      <main>
        {view === "adoption" && <AdoptionView />}
        {view === "velocity" && <VelocityView />}
      </main>
    </div>
  );
}
