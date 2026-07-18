// SPDX-License-Identifier: Apache-2.0
// Read-only console. Every network call is a GET against the public REST
// API with the viewer's own token — nothing else, ever (M11 hard rule).
"use strict";

const $ = (sel, el = document) => el.querySelector(sel);
const view = $("#view");

function token() { return sessionStorage.getItem("rabi-token") || ""; }

async function api(path) {
  const resp = await fetch(path, {
    method: "GET",
    headers: { Authorization: "Bearer " + token() },
  });
  if (!resp.ok) throw new Error(`${path}: HTTP ${resp.status}`);
  return resp.json();
}

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") node.className = v;
    else if (k === "text") node.textContent = v;
    else node.setAttribute(k, v);
  }
  for (const c of children) node.append(c);
  return node;
}

function age(iso) {
  if (!iso) return "unknown";
  const ms = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours} h ${mins % 60} min ago`;
  return `${Math.floor(hours / 24)} days ago`;
}

function fail(err) {
  view.replaceChildren(el("p", { class: "error", text: String(err) }));
}

// ---- fleet ----------------------------------------------------------------

async function renderFleet() {
  const data = await api("/v1alpha1/targets");
  const cards = (data.targets || []).map((t) => {
    const caps = t.capabilities || {};
    const info = caps.target || {};
    const state = t.state || {};
    const cal = state.calibration || {};
    const metrics = (cal.metrics || []).slice(0, 12);
    const rows = metrics.map((m) =>
      el("tr", {},
        el("td", { text: m.name }),
        el("td", { text: Number(m.value).toPrecision(3) }),
        el("td", { text: (m.qubits || []).join(",") }),
        el("td", { class: "muted methodology", text: m.methodology || "—" })));
    return el("article", { class: "card target" },
      el("h2", { text: t.name }),
      el("p", { class: "muted" },
        `${info.technology || "?"} · ${caps.numQubits || "?"}q · ` +
        `${info.simulator ? "simulator" : "hardware"}` +
        `${caps.cloudQueue ? " · cloud queue" : ""} · ${state.status || "?"}`),
      el("p", {},
        el("strong", { text: "calibration " }),
        el("span", { class: "cal-age", text: age(cal.measuredAt) }),
        el("span", { class: "muted", text: ` · snapshot ${cal.snapshotId || "?"} · source ${cal.source || "?"}` })),
      metrics.length
        ? el("table", {},
            el("thead", {}, el("tr", {},
              el("th", { text: "metric" }), el("th", { text: "value" }),
              el("th", { text: "qubits" }), el("th", { text: "methodology" }))),
            el("tbody", {}, ...rows))
        : el("p", { class: "muted", text: "no calibration metrics" }));
  });
  view.replaceChildren(el("h1", { text: "Fleet" }), ...cards);
}

// ---- jobs -----------------------------------------------------------------

async function renderJobs() {
  const tenant = sessionStorage.getItem("rabi-tenant") || "";
  const q = tenant ? `?tenant=${encodeURIComponent(tenant)}` : "";
  const data = await api(`/v1alpha1/jobs${q}`);
  const rows = (data.jobs || []).map((j) => {
    const st = j.status || {};
    const link = el("a", { href: `#job/${j.jobId}`, text: j.jobId.slice(0, 8) });
    return el("tr", {},
      el("td", {}, link),
      el("td", { text: (j.quantumJob?.metadata?.name) || "?" }),
      el("td", { text: j.tenant }),
      el("td", { class: `phase phase-${st.phase}`, text: st.phase || "?" }),
      el("td", { class: "muted", text: st.boundTarget || "—" }));
  });
  const filter = el("input", {
    id: "tenant-filter", placeholder: "filter by tenant (empty = all you can see)",
    value: tenant, size: "40",
  });
  filter.addEventListener("change", () => {
    sessionStorage.setItem("rabi-tenant", filter.value.trim());
    renderJobs().catch(fail);
  });
  view.replaceChildren(
    el("h1", { text: "Jobs" }), filter,
    el("table", { class: "jobs" },
      el("thead", {}, el("tr", {},
        el("th", { text: "id" }), el("th", { text: "name" }),
        el("th", { text: "tenant" }), el("th", { text: "phase" }),
        el("th", { text: "target" }))),
      el("tbody", {}, ...rows)));
}

// ---- placement audit ("why did my job land there") ------------------------

async function renderJob(jobId) {
  const j = await api(`/v1alpha1/jobs/${jobId}`);
  const st = j.status || {};
  const p = st.placement || {};
  const parts = [
    el("h1", { text: `Job ${jobId.slice(0, 8)}` }),
    el("p", {},
      el("span", { class: `phase phase-${st.phase}`, text: st.phase || "?" }),
      el("span", { class: "muted", text: `  ${j.tenant} · ${(j.quantumJob?.metadata?.name) || ""}` })),
  ];

  if (st.boundTarget) {
    parts.push(el("h2", { text: "Why it landed there" }));
    const facts = [
      ["target", st.boundTarget],
      ["policy", p.policy],
      ["calibration snapshot", p.calibrationSnapshot],
      ["predicted wait", p.predicted?.waitSeconds != null ? `${p.predicted.waitSeconds}s` : null],
      ["predicted success", p.predicted?.successProbability != null
        ? Number(p.predicted.successProbability).toFixed(3) : null],
      ["onConflict", p.onConflict],
      ["decision horizon", p.decisionHorizon ? `${p.decisionHorizon} (${p.horizonModel})` : null],
    ].filter(([, v]) => v != null && v !== "");
    parts.push(el("dl", { class: "audit" },
      ...facts.flatMap(([k, v]) => [el("dt", { text: k }), el("dd", { text: String(v) })])));
    if (p.floorsRelaxed) {
      const details = (p.relaxedFloors || []).map((f) =>
        el("li", { text: `${f.floor}: ${f.aggregate} value ${f.actual} exceeds floor ${f.limit}` }));
      parts.push(el("div", { class: "warn" },
        el("strong", { text: "Quality floors relaxed (prefer-deadline): " }),
        el("ul", {}, ...details)));
    }
    if (p.reason) parts.push(el("p", { class: "reason", text: p.reason }));
    const rejected = p.rejected || [];
    if (rejected.length) {
      parts.push(el("h2", { text: `Rejected targets (${rejected.length})` }));
      parts.push(el("ul", { class: "rejected" },
        ...rejected.map((r) => el("li", {},
          el("strong", { text: r.target + ": " }), el("span", { text: r.reason })))));
    }
  } else {
    parts.push(el("p", { class: "muted", text: "not placed yet" }));
  }

  const conditions = st.conditions || [];
  if (conditions.length) {
    parts.push(el("h2", { text: "Conditions" }));
    parts.push(el("ul", {}, ...conditions.map((c) =>
      el("li", {}, el("strong", { text: `${c.type}=${c.status} ` }),
        el("span", { class: "muted", text: c.message || c.reason || "" })))));
  }
  if (st.usage?.length) {
    parts.push(el("h2", { text: "Usage" }));
    parts.push(el("ul", {}, ...st.usage.map((u) =>
      el("li", { text: `${u.amount} ${u.unit}` }))));
  }
  view.replaceChildren(...parts);
}

// ---- usage ----------------------------------------------------------------

async function renderUsage() {
  const tenant = sessionStorage.getItem("rabi-tenant") || "";
  const parts = [el("h1", { text: "Usage" })];
  const filter = el("input", {
    placeholder: "tenant (required)", value: tenant, size: "40",
  });
  filter.addEventListener("change", () => {
    sessionStorage.setItem("rabi-tenant", filter.value.trim());
    renderUsage().catch(fail);
  });
  parts.push(filter);
  if (tenant) {
    const data = await api(`/v1alpha1/usage?tenant=${encodeURIComponent(tenant)}`);
    const rows = (data.usage || []).map((u) =>
      el("tr", {},
        el("td", { text: u.target }), el("td", { text: u.unit }),
        el("td", { text: String(u.amount) })));
    parts.push(el("table", {},
      el("thead", {}, el("tr", {},
        el("th", { text: "target" }), el("th", { text: "unit" }),
        el("th", { text: "amount (native units)" }))),
      el("tbody", {}, ...rows)));
  } else {
    parts.push(el("p", { class: "muted", text: "enter a tenant to see native-unit usage" }));
  }
  view.replaceChildren(...parts);
}

// ---- shell ----------------------------------------------------------------

async function route() {
  if (!token()) {
    $("#token-gate").hidden = false;
    view.hidden = true;
    return;
  }
  $("#token-gate").hidden = true;
  view.hidden = false;
  const hash = location.hash.replace(/^#/, "") || "fleet";
  document.querySelectorAll("[data-nav]").forEach((a) =>
    a.classList.toggle("active", hash.startsWith(a.dataset.nav)));
  try {
    if (hash === "fleet") await renderFleet();
    else if (hash === "jobs") await renderJobs();
    else if (hash.startsWith("job/")) await renderJob(hash.slice(4));
    else if (hash === "usage") await renderUsage();
    else await renderFleet();
  } catch (err) {
    fail(err);
  }
}

$("#token-save").addEventListener("click", () => {
  sessionStorage.setItem("rabi-token", $("#token-input").value.trim());
  route();
});
window.addEventListener("hashchange", route);
route();
