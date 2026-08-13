// Chart.js lifecycle glue for the Dashboard's chart canvases
// (internal/gui/ui/dashboard_stats.templ's DashboardCharts). See
// dev-docs/chartjs.md for the design this follows. Two things a plain
// `new Chart(...)` call doesn't handle on its own in this codebase:
//
// 1. Chart.js has no reactivity of its own — nothing here watches the DOM
//    or chart.data for changes. DashboardCharts renders its 5 canvases
//    exactly once (never touched by DashboardActivity's own 60s outerHTML
//    poll, unlike the old design); #chart-poller issues its own JSON poll
//    of /api/dashboard/charts (hx-swap="none", pure transport, no DOM
//    effect from htmx itself), and ChartManager below is what actually
//    mutates each chart's `data`/`options` in place and calls
//    `chart.update()` on every response — that's Chart.js's real supported
//    "live update" path (see dev-docs/chartjs.md), and it's what gives the
//    animated transitions a destroy/recreate cycle never could.
// 2. Chart.js renders to a bitmap canvas, so it can't pick up BrambleGate's
//    CSS custom-property theme swap (light/dark) the way every other
//    element on the page does automatically. Colors are resolved from this
//    file's own light/dark tables on every update (not just at init —
//    category counts can change poll to poll, e.g. the "Other" bucket),
//    matched to the same data-theme attribute theme.js already manages.
//    On a theme toggle, ChartManager re-applies the last-seen payload with
//    the new theme's colors and updates instantly (no animation — a manual
//    color-scheme flip isn't the kind of change animation helps with).
//
// Server-rendered templ only supplies bare canvases (an id + data-chart
// role) — no Chart.js config, no color, no JS source: all chart-shape/color
// decisions live here, in one place, not duplicated per template. Chart
// *data* comes from /api/dashboard/charts (DashboardChartsPayload,
// handlers_ui.go) — also plain data only, same split.
(function () {
  // Both validated against BrambleGate's own card surfaces (dataviz
  // skill's validate_palette.js: light vs #F3F2F2, dark vs #221E17). Order
  // matters: assigned to series in this fixed order, never cycled/
  // reassigned per filter (the skill's categorical rule) — a series keeps
  // its color across refreshes because the JSON payload (handlers_ui.go)
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

  function legendOptions(t, extra) {
    return Object.assign({ labels: { color: t.ink.secondary, font: { size: 11 } } }, extra);
  }

  // applyTimeSeries: a single-series line chart (DashboardChartsResponse's
  // Series field) — one series needs no legend (the card title already
  // names it, per the skill's accessibility rule).
  function applyTimeSeries(chart, payload, t) {
    chart.data.labels = payload.labels || [];
    chart.data.datasets = [
      {
        data: payload.values || [],
        borderColor: t.categorical[0],
        backgroundColor: t.categorical[0] + "33",
        fill: true,
        borderWidth: 2,
        pointRadius: 0,
        tension: 0.2,
      },
    ];
    chart.options.plugins.legend = { display: false };
    chart.options.scales = {
      x: { ticks: { color: t.ink.muted, maxTicksLimit: 8, font: { size: 10 } }, grid: { display: false } },
      y: {
        beginAtZero: true,
        ticks: { color: t.ink.muted, precision: 0, font: { size: 10 } },
        grid: { color: t.ink.grid },
      },
    };
  }

  // applyDoughnut: a categorical breakdown (Sources/QTypes/CacheActivity) —
  // always shown with a legend (>=2 series) and, per the skill's relief
  // rule, direct percentage labels aren't added on the arcs themselves
  // (Chart.js has no built-in support without a plugin) — the legend text
  // is the visible-label mitigation for the categorical palette's
  // light-mode contrast WARN.
  function applyDoughnut(chart, payload, t) {
    const labels = payload.labels || [];
    const values = payload.values || [];
    const colors = labels.map(function (_, i) {
      return t.categorical[i % t.categorical.length];
    });
    chart.data.labels = labels;
    chart.data.datasets = [{ data: values, backgroundColor: colors, borderWidth: 0 }];
    chart.options.cutout = "60%";
    chart.options.plugins.legend = legendOptions(t, { position: "right" });
  }

  // applyStackedBar: pihole-style "Client Activity" chart — one bar per
  // time bucket, stacked by client (payload.series), folded "Other" series
  // included as just another series (already summed server-side). Always
  // shown with a legend, same rule as the doughnut applier, since there's
  // always >=2 series once this card renders at all (a lone client still
  // gets an implicit "Other" of zero, harmless either way). Dataset count
  // can change poll to poll (clients come and go), so the whole datasets
  // array is replaced each time rather than mutated per-index.
  function applyStackedBar(chart, payload, t) {
    const datasets = (payload.series || []).map(function (s, i) {
      return {
        label: s.name,
        data: s.values,
        backgroundColor: t.categorical[i % t.categorical.length],
      };
    });
    chart.data.labels = payload.labels || [];
    chart.data.datasets = datasets;
    chart.options.plugins.legend = legendOptions(t, { position: "right" });
    chart.options.scales = {
      x: { stacked: true, ticks: { color: t.ink.muted, maxTicksLimit: 8, font: { size: 10 } }, grid: { display: false } },
      y: {
        stacked: true,
        beginAtZero: true,
        ticks: { color: t.ink.muted, precision: 0, font: { size: 10 } },
        grid: { color: t.ink.grid },
      },
    };
  }

  // CHARTS: the fixed set of canvases DashboardCharts renders, each mapped
  // to its Chart.js type, its key in DashboardChartsResponse's JSON, and
  // the applier that writes payload+theme into an existing chart in place.
  const CHARTS = {
    "chart-series": { type: "line", key: "series", apply: applyTimeSeries },
    "chart-sources": { type: "doughnut", key: "sources", apply: applyDoughnut },
    "chart-qtypes": { type: "doughnut", key: "qtypes", apply: applyDoughnut },
    "chart-cache-activity": { type: "doughnut", key: "cacheActivity", apply: applyDoughnut },
    "chart-client-activity": { type: "bar", key: "clientActivity", apply: applyStackedBar },
  };

  const ChartManager = {
    instances: new Map(), // canvas id -> Chart instance
    lastPayload: null, // last /api/dashboard/charts response, for theme re-apply

    // init: create one empty Chart per known canvas id present in `root`,
    // exactly once each (a canvas already in `instances` is left alone —
    // DashboardCharts never re-renders its canvases, so this only ever
    // needs to run on the initial DOMContentLoaded scan in practice, but
    // staying idempotent costs nothing).
    init(root) {
      if (typeof Chart === "undefined") return;
      root.querySelectorAll("canvas[data-chart]").forEach((canvas) => {
        if (this.instances.has(canvas.id)) return;
        const meta = CHARTS[canvas.id];
        if (!meta) return;
        try {
          this.instances.set(
            canvas.id,
            new Chart(canvas, { type: meta.type, data: { labels: [], datasets: [] }, options: baseOptions(theme()) })
          );
        } catch (e) {
          // Isolate one bad canvas so it can't abort the rest of this
          // batch (forEach doesn't catch — an uncaught throw here would
          // skip every canvas still queued after this one).
          console.error("charts.js: failed to init chart", canvas.id, e);
        }
      });
    },

    // applyPayload: fan one /api/dashboard/charts response out to every
    // tracked chart instance, mutating each chart's data/options in place
    // and calling chart.update() — the animated-transition path.
    applyPayload(payload) {
      if (!payload) return;
      this.lastPayload = payload;
      this._apply(payload, "default");
    },

    // applyTheme: re-run the last-seen payload through the appliers with
    // freshly computed theme colors, updating instantly (no animation) —
    // called on a light/dark toggle click. No-ops if the toggle fires
    // before the first poll response has arrived.
    applyTheme() {
      if (!this.lastPayload) return;
      this._apply(this.lastPayload, "none");
    },

    _apply(payload, mode) {
      const t = theme();
      this.instances.forEach((chart, id) => {
        const meta = CHARTS[id];
        const data = payload[meta.key];
        if (!data) return;
        try {
          meta.apply(chart, data, t);
          chart.update(mode);
        } catch (e) {
          console.error("charts.js: failed to update chart", id, e);
        }
      });
    },
  };

  document.addEventListener("DOMContentLoaded", function () {
    ChartManager.init(document);
    document.querySelectorAll("[data-theme-btn]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        ChartManager.applyTheme();
      });
    });
  });

  // #chart-poller's own hx-get (hx-swap="none" — htmx never touches the DOM
  // for this request, it's pure transport); every response is the full
  // DashboardChartsResponse JSON, fanned out to all 5 charts above. htmx
  // dispatches its custom events ON the requesting element and lets them
  // bubble, so `evt.target` (standard DOM event target) is the reliable,
  // version-independent way to identify #chart-poller's own request here —
  // htmx 2.x's htmx:afterRequest detail object carries `xhr`/`target`(the
  // swap target)/`requestConfig`/etc, but no top-level `.elt` or
  // `.successful` the way some docs/examples assume; check the xhr status
  // directly instead.
  document.addEventListener("htmx:afterRequest", function (evt) {
    if (!evt.target || evt.target.id !== "chart-poller") return;
    const xhr = evt.detail && evt.detail.xhr;
    if (!xhr || xhr.status < 200 || xhr.status >= 300) return;
    let payload;
    try {
      payload = JSON.parse(xhr.responseText);
    } catch (e) {
      console.error("charts.js: failed to parse chart payload", e);
      return;
    }
    ChartManager.applyPayload(payload);
  });
})();
