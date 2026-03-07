import { useState } from "preact/hooks";
import { AdoptionView } from "./views/AdoptionView";

type View = "adoption";

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
        </nav>
      </header>
      <main>
        {view === "adoption" && <AdoptionView />}
      </main>
    </div>
  );
}
