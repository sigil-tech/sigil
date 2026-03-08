import { useEffect, useRef, useState } from "preact/hooks";
import { Chart, registerables } from "chart.js";
import { fetchVelocity, type VelocityData } from "../api";

Chart.register(...registerables);

const TIER_LABELS = ["Observer", "Explorer", "Integrator", "Native"];
const TIER_COLORS = ["#94a3b8", "#60a5fa", "#34d399", "#a78bfa"];

export function VelocityView() {
  const [data, setData] = useState<VelocityData | null>(null);
  const [error, setError] = useState("");
  const buildChartRef = useRef<HTMLCanvasElement>(null);
  const scatterChartRef = useRef<HTMLCanvasElement>(null);
  const buildChart = useRef<Chart | null>(null);
  const scatterChart = useRef<Chart | null>(null);

  useEffect(() => {
    fetchVelocity()
      .then(setData)
      .catch((e: Error) => setError(e.message));
  }, []);

  useEffect(() => {
    if (!data || !buildChartRef.current || !scatterChartRef.current) return;

    buildChart.current?.destroy();
    scatterChart.current?.destroy();

    // Build success rate by tier
    buildChart.current = new Chart(buildChartRef.current, {
      type: "bar",
      data: {
        labels: data.data.map((d) => TIER_LABELS[d.tier]),
        datasets: [
          {
            label: "Avg Build Success Rate",
            data: data.data.map((d) => d.avg_build_rate * 100),
            backgroundColor: data.data.map((d) => TIER_COLORS[d.tier]),
          },
        ],
      },
      options: {
        responsive: true,
        scales: {
          y: { title: { display: true, text: "Success Rate (%)" }, max: 100 },
        },
        plugins: {
          title: { display: true, text: "Build Success Rate by Adoption Tier" },
        },
      },
    });

    // Scatter: events vs tier
    scatterChart.current = new Chart(scatterChartRef.current, {
      type: "scatter",
      data: {
        datasets: [
          {
            label: "Avg Events vs Tier",
            data: data.data.map((d) => ({ x: d.tier, y: d.avg_events })),
            backgroundColor: "#60a5fa",
            pointRadius: 8,
          },
        ],
      },
      options: {
        responsive: true,
        scales: {
          x: {
            title: { display: true, text: "Adoption Tier" },
            ticks: {
              callback: (val) => TIER_LABELS[val as number] || val,
            },
          },
          y: { title: { display: true, text: "Avg Events" } },
        },
        plugins: {
          title: { display: true, text: "Event Volume vs Adoption Tier" },
        },
      },
    });

    return () => {
      buildChart.current?.destroy();
      scatterChart.current?.destroy();
    };
  }, [data]);

  if (error) return <div class="error">Error: {error}</div>;
  if (!data) return <div>Loading velocity data...</div>;

  return (
    <div class="view">
      <h2>Developer Velocity Correlation</h2>
      <p class="disclaimer">
        {data.disclaimer}
      </p>
      <canvas ref={buildChartRef} />
      <canvas ref={scatterChartRef} />
    </div>
  );
}
