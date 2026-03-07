import { useEffect, useRef, useState } from "preact/hooks";
import { Chart, registerables } from "chart.js";
import { fetchAdoption, type AdoptionData } from "../api";

Chart.register(...registerables);

const TIER_LABELS = ["Observer", "Explorer", "Integrator", "Native"];
const TIER_COLORS = ["#94a3b8", "#60a5fa", "#34d399", "#a78bfa"];

export function AdoptionView() {
  const [data, setData] = useState<AdoptionData | null>(null);
  const [error, setError] = useState("");
  const chartRef = useRef<HTMLCanvasElement>(null);
  const chartInstance = useRef<Chart | null>(null);

  useEffect(() => {
    fetchAdoption()
      .then(setData)
      .catch((e: Error) => setError(e.message));
  }, []);

  useEffect(() => {
    if (!data || !chartRef.current) return;

    if (chartInstance.current) {
      chartInstance.current.destroy();
    }

    const dates = [...new Set(data.data.map((d) => d.date))];
    const datasets = [0, 1, 2, 3].map((tier) => ({
      label: TIER_LABELS[tier],
      data: dates.map((date) => {
        const row = data.data.find((d) => d.date === date && d.tier === tier);
        return row ? row.count : 0;
      }),
      backgroundColor: TIER_COLORS[tier],
    }));

    chartInstance.current = new Chart(chartRef.current, {
      type: "bar",
      data: { labels: dates, datasets },
      options: {
        responsive: true,
        scales: {
          x: { stacked: true },
          y: { stacked: true, title: { display: true, text: "Engineers" } },
        },
        plugins: {
          title: { display: true, text: "Adoption Tier Distribution Over Time" },
        },
      },
    });

    return () => {
      chartInstance.current?.destroy();
    };
  }, [data]);

  if (error) return <div class="error">Error: {error}</div>;
  if (!data) return <div>Loading adoption data...</div>;

  return (
    <div class="view">
      <h2>AI Adoption Analytics</h2>
      <canvas ref={chartRef} />
    </div>
  );
}
