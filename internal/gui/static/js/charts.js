// Chart.js lifecycle glue for the Dashboard's Activity section
// (internal/gui/ui/dashboard_stats.templ). Two things a plain
// `new Chart(...)` call doesn't handle on its own in this codebase:
//
// 1. htmx swaps the Activity section wholesale on every poll (outerHTML,
//    same as the Query Log grid) — a Chart.js instance bound to a <canvas>
//    that just got replaced is stale, and re-initializing without
//    destroying it first throws "Canvas is already in use". Track
//    instances by canvas id and destroy-before-create.
// 2. Chart.js renders to a bitmap canvas, so it can't pick up BrambleGate's
//    CSS custom-property theme swap (light/dark) the way every other
//    element on the page does automatically. Colors are resolved from this
//    file's own light/dark tables at init time, matched to the same
//    data-theme attribute theme.js already manages, and charts are
//    re-initialized whenever the user flips the toggle.
//
// Server-rendered templ only supplies plain data (a role + a JSON payload
// of labels/values via data-chart/data-payload) — no Chart.js config, no
// color, no JS source, in either attribute: all chart-shape/color decisions
// live here, in one place, not duplicated per template.
(function () {
  // Both validated against BrambleGate's own card surfaces (dataviz
  // skill's validate_palette.js: light vs #F3F2F2, dark vs #221E17). Order
  // matters: assigned to series in this fixed order, never cycled/
  // reassigned per filter (the skill's categorical rule) — a series keeps
  // its color across refreshes because chartPayload (handlers_ui.go)
  // always emits categories in the same order.
  const CATEGORICAL = {
    light: ["#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"],
    dark: ["#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"],
  };
  const INK = {
    light: { primary: "#0b0b0b", secondary: "#52514e", muted: "#898781", grid: "#e1e0d9" },
    dark: { primary: "#ffffff", secondary: "#c3c2b7", muted: "#898781", grid: "#2c2c2a" },
  };

  function isDark() {
    const t = document.documentElement.getAttribute("data-theme");
    if (t === "dark") return true;
    if (t === "light") return false;
    return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  }

  function theme() {
    const mode = isDark() ? "dark" : "light";
    return { categorical: CATEGORICAL[mode], ink: INK[mode] };
  }

  // Shared options: no dual axes anywhere (per the skill's #1 rule), thin
  // gridlines in the muted ink, no chart title (the surrounding card's <h2>
  // is the title).
  function baseOptions(t) {
    return {
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { labels: { color: t.ink.secondary, font: { size: 11 } } } },
    };
  }

  // buildTimeSeries: a single-series line chart (Log.RecentSeries) — one
  // series needs no legend (the card title already names it, per the
  // skill's accessibility rule).
  function buildTimeSeries(payload, t) {
    return {
      type: "line",
      data: {
        labels: payload.labels,
        datasets: [
          {
            data: payload.values,
            borderColor: t.categorical[0],
            backgroundColor: t.categorical[0] + "33",
            fill: true,
            borderWidth: 2,
            pointRadius: 0,
            tension: 0.2,
          },
        ],
      },
      options: Object.assign(baseOptions(t), {
        plugins: Object.assign(baseOptions(t).plugins, { legend: { display: false } }),
        scales: {
          x: { ticks: { color: t.ink.muted, maxTicksLimit: 8, font: { size: 10 } }, grid: { display: false } },
          y: {
            beginAtZero: true,
            ticks: { color: t.ink.muted, precision: 0, font: { size: 10 } },
            grid: { color: t.ink.grid },
          },
        },
      }),
    };
  }

  // buildDoughnut: a categorical breakdown (Verdict/Source/QType) — always
  // shown with a legend (>=2 series) and, per the skill's relief rule,
  // direct percentage labels aren't added on the arcs themselves (Chart.js
  // has no built-in support without a plugin) — the legend text is the
  // visible-label mitigation for the categorical palette's light-mode
  // contrast WARN.
  function buildDoughnut(payload, t) {
    const colors = payload.labels.map(function (_, i) {
      return t.categorical[i % t.categorical.length];
    });
    return {
      type: "doughnut",
      data: { labels: payload.labels, datasets: [{ data: payload.values, backgroundColor: colors, borderWidth: 0 }] },
      options: Object.assign(baseOptions(t), {
        cutout: "60%",
        plugins: Object.assign(baseOptions(t).plugins, { legend: { position: "right", labels: baseOptions(t).plugins.legend.labels } }),
      }),
    };
  }

  const BUILDERS = { timeseries: buildTimeSeries, doughnut: buildDoughnut };

  const instances = new Map(); // canvas id -> Chart instance

  function initCharts(root) {
    if (typeof Chart === "undefined") return;
    root.querySelectorAll("canvas[data-chart]").forEach(function (canvas) {
      const existing = instances.get(canvas.id);
      if (existing) {
        existing.destroy();
        instances.delete(canvas.id);
      }
      const role = canvas.dataset.chart;
      const build = BUILDERS[role];
      if (!build) return;
      let payload;
      try {
        payload = JSON.parse(canvas.dataset.payload || "{}");
      } catch (e) {
        return;
      }
      instances.set(canvas.id, new Chart(canvas, build(payload, theme())));
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    initCharts(document);
    document.querySelectorAll("[data-theme-btn]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        initCharts(document);
      });
    });
  });
  document.body.addEventListener("htmx:afterSwap", function (evt) {
    initCharts(evt.detail.target);
  });
})();