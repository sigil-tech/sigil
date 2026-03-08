import { useState } from "preact/hooks";
import { AdoptionView } from "./views/AdoptionView";
import { VelocityView } from "./views/VelocityView";
import { CostView } from "./views/CostView";
import { ComplianceView } from "./views/ComplianceView";

type View = "adoption" | "velocity" | "cost" | "compliance";

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
          <button onClick={() => setView("cost")} class={view === "cost" ? "active" : ""}>
            Cost
          </button>
          <button onClick={() => setView("compliance")} class={view === "compliance" ? "active" : ""}>
            Compliance
          </button>
        </nav>
      </header>
      <main>
        {view === "adoption" && <AdoptionView />}
        {view === "velocity" && <VelocityView />}
        {view === "cost" && <CostView />}
        {view === "compliance" && <ComplianceView />}
      </main>
    </div>
  );
}
