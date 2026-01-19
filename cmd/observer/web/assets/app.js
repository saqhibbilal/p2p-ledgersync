const $ = (id) => document.getElementById(id);

let lastSnap = null;
let graphHit = { edges: [], nodes: new Map() };

function fmtTime(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  const now = Date.now();
  const diff = Math.max(0, now - d.getTime());
  const sec = Math.floor(diff / 1000);
  if (sec < 2) return "now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  return `${hr}h ago`;
}

function shortHash(hex) {
  if (!hex) return "—";
  if (hex.length <= 16) return hex;
  return `${hex.slice(0, 10)}…${hex.slice(-6)}`;
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[c]));
}

function statusPill(status) {
  const s = status || "offline";
  const label = s === "synced" ? "Synced" : s === "syncing" ? "Syncing" : "Offline";
  return `<span class="pill ${s}"><span class="dot"></span>${label}</span>`;
}

function renderStats(snap) {
  const el = $("stats");
  const blocks = [
    { k: "Network tip", v: snap.networkTip ?? 0 },
    { k: "Nodes", v: `${snap.onlineCount ?? 0}/${snap.totalCount ?? 0}` },
    { k: "Synced", v: snap.syncedCount ?? 0 },
    { k: "Syncing", v: snap.syncingCount ?? 0 },
    { k: "Offline", v: snap.offlineCount ?? 0 },
  ];
  el.innerHTML = blocks
    .map(
      (b) => `<div class="stat"><div class="k">${b.k}</div><div class="v">${b.v}</div></div>`
    )
    .join("");
}

function renderNodes(snap) {
  const tbody = $("nodesTbody");
  const rows = (snap.nodes || []).map((n) => {
    const id = n.id || "—";
    const height = n.tipHeight ?? 0;
    const lag = n.lag ?? 0;
    const sent = n.blocksSent ?? 0;
    const recv = n.blocksReceived ?? 0;
    const lastSeen = n.lastSeen ? fmtTime(n.lastSeen) : "—";
    const hash = n.tipHash ? shortHash(n.tipHash) : "—";
    return `<tr>
      <td>${id}<div class="muted">${n.addr || ""}</div></td>
      <td>${statusPill(n.status)}</td>
      <td class="num">${height}</td>
      <td class="num">${lag}</td>
      <td class="hash" title="${n.tipHash || ""}">${hash}</td>
      <td class="num">${sent}</td>
      <td class="num">${recv}</td>
      <td>${lastSeen}</td>
    </tr>`;
  });
  tbody.innerHTML = rows.join("");
}

function pickStatusColor(status) {
  if (status === "synced") return "#2bd576";
  if (status === "syncing") return "#f5c542";
  return "#ff4d4d";
}

function fmtAbsTime(t) {
  if (!t) return "—";
  const d = new Date(t);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString();
}

function renderEventLog(snap) {
  const list = $("logList");
  if (!list) return;

  const search = ($("logSearch")?.value || "").trim().toLowerCase();
  const allowed = new Set();
  if ($("fPeer")?.checked) allowed.add("peer");
  if ($("fBlocks")?.checked) allowed.add("blocks");
  if ($("fTip")?.checked) allowed.add("tip");

  const events = snap.events || [];
  const rows = [];
  for (const e of events) {
    const type = e.type || "info";
    if (allowed.size && !allowed.has(type)) continue;

    const needle = `${e.nodeId || ""} ${e.nodeAddr || ""} ${e.peerId || ""} ${e.peerAddr || ""} ${e.message || ""}`.toLowerCase();
    if (search && !needle.includes(search)) continue;

    const nodeLabel = e.nodeId || e.nodeAddr || "—";
    const msg = e.message || "";
    const peer = e.peerId || e.peerAddr ? ` → ${e.peerId || e.peerAddr}` : "";

    rows.push(`<div class="logRow">
      <div class="muted" title="${esc(e.at || "")}">${esc(fmtTime(e.at))}</div>
      <div><span class="tag ${esc(type)}">${esc(type)}</span></div>
      <div title="${esc(e.nodeAddr || "")}">${esc(nodeLabel)}${esc(peer)}</div>
      <div>${esc(msg)}</div>
    </div>`);
  }

  list.innerHTML = rows.length ? rows.join("") : `<div class="logRow"><div class="muted">—</div><div><span class="tag tip">info</span></div><div>—</div><div class="muted">No events match the filter.</div></div>`;
}

function distToSegment(px, py, ax, ay, bx, by) {
  const vx = bx - ax;
  const vy = by - ay;
  const wx = px - ax;
  const wy = py - ay;
  const c1 = vx * wx + vy * wy;
  if (c1 <= 0) return Math.hypot(px - ax, py - ay);
  const c2 = vx * vx + vy * vy;
  if (c2 <= c1) return Math.hypot(px - bx, py - by);
  const t = c1 / c2;
  const ix = ax + t * vx;
  const iy = ay + t * vy;
  return Math.hypot(px - ix, py - iy);
}

function drawArrow(ctx, ax, ay, bx, by, color, width, highlight) {
  const headLen = highlight ? 12 : 10;
  const headAngle = Math.PI / 7;

  // line
  ctx.beginPath();
  ctx.moveTo(ax, ay);
  ctx.lineTo(bx, by);
  ctx.strokeStyle = color;
  ctx.lineWidth = highlight ? width + 1 : width;
  ctx.stroke();

  // head
  const ang = Math.atan2(by - ay, bx - ax);
  const hx1 = bx - headLen * Math.cos(ang - headAngle);
  const hy1 = by - headLen * Math.sin(ang - headAngle);
  const hx2 = bx - headLen * Math.cos(ang + headAngle);
  const hy2 = by - headLen * Math.sin(ang + headAngle);
  ctx.beginPath();
  ctx.moveTo(bx, by);
  ctx.lineTo(hx1, hy1);
  ctx.lineTo(hx2, hy2);
  ctx.closePath();
  ctx.fillStyle = color;
  ctx.fill();
}

function drawGraph(snap) {
  const canvas = $("graph");
  const ctx = canvas.getContext("2d");
  // Handle high-DPI
  const rect = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.floor(rect.width * dpr);
  canvas.height = Math.floor(rect.height * dpr);
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

  const w = rect.width;
  const h = rect.height;

  ctx.clearRect(0, 0, w, h);

  const nodes = snap.nodes || [];
  const edges = snap.edges || [];

  const centerX = w / 2;
  const centerY = h / 2;
  const radius = Math.min(w, h) * 0.34;

  // Layout: stable circular ordering by node id/addr
  const ordered = [...nodes].sort((a, b) => (a.id || a.addr).localeCompare(b.id || b.addr));
  const pos = new Map(); // addr -> {x,y}
  ordered.forEach((n, i) => {
    const ang = (Math.PI * 2 * i) / Math.max(1, ordered.length) - Math.PI / 2;
    const x = centerX + radius * Math.cos(ang);
    const y = centerY + radius * Math.sin(ang);
    pos.set(n.addr, { x, y });
  });

  // Hit model for hover
  graphHit = { edges: [], nodes: pos };

  // Edges (directed)
  const nodeRadius = 16;
  const baseWidth = 1;
  const hover = window.__hoverEdgeKey || null;

  edges.forEach((e) => {
    const a = pos.get(e.fromAddr);
    const b = pos.get(e.toAddr);
    if (!a || !b) return;

    const dx = b.x - a.x;
    const dy = b.y - a.y;
    const dist = Math.hypot(dx, dy) || 1;
    const ux = dx / dist;
    const uy = dy / dist;

    // trim to avoid drawing into node circles
    const ax = a.x + ux * nodeRadius;
    const ay = a.y + uy * nodeRadius;
    const bx = b.x - ux * nodeRadius;
    const by = b.y - uy * nodeRadius;

    const key = `${e.fromAddr}->${e.toAddr}`;
    const isHover = hover === key;
    const color = e.up ? "rgba(86,166,255,.55)" : "rgba(255,255,255,.14)";
    drawArrow(ctx, ax, ay, bx, by, isHover ? "rgba(86,166,255,.85)" : color, baseWidth, isHover);

    graphHit.edges.push({
      key,
      fromId: e.fromId || e.fromAddr,
      toId: e.toId || e.toAddr,
      fromAddr: e.fromAddr,
      toAddr: e.toAddr,
      up: !!e.up,
      lastAt: e.lastAt,
      ax,
      ay,
      bx,
      by,
    });
  });

  // Nodes
  ordered.forEach((n) => {
    const p = pos.get(n.addr);
    if (!p) return;
    const color = pickStatusColor(n.status);

    // Outer glow
    ctx.beginPath();
    ctx.arc(p.x, p.y, 18, 0, Math.PI * 2);
    ctx.fillStyle = "rgba(0,0,0,.35)";
    ctx.fill();

    ctx.beginPath();
    ctx.arc(p.x, p.y, 14, 0, Math.PI * 2);
    ctx.fillStyle = "rgba(15,22,36,.95)";
    ctx.fill();
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.stroke();

    // Label
    ctx.fillStyle = "#e6eefc";
    ctx.font = "12px Quantico, system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const label = n.id || n.addr || "node";
    ctx.fillText(label, p.x, p.y + 18);

    // Height
    ctx.fillStyle = "rgba(159,176,207,.95)";
    ctx.font = "11px Quantico, system-ui, sans-serif";
    ctx.textBaseline = "bottom";
    ctx.fillText(`h=${n.tipHeight ?? 0}`, p.x, p.y - 18);
  });
}

function renderAll(snap) {
  lastSnap = snap;
  renderStats(snap);
  renderNodes(snap);
  drawGraph(snap);
  renderEventLog(snap);
  $("footerStatus").textContent = `Live • ${new Date(snap.at || Date.now()).toLocaleTimeString()}`;
}

function setupLogControls() {
  const ids = ["logSearch", "fPeer", "fBlocks", "fTip"];
  for (const id of ids) {
    const el = $(id);
    if (!el) continue;
    el.addEventListener("input", () => lastSnap && renderEventLog(lastSnap));
    el.addEventListener("change", () => lastSnap && renderEventLog(lastSnap));
  }
  const reset = $("logReset");
  if (reset) {
    reset.addEventListener("click", () => {
      if ($("logSearch")) $("logSearch").value = "";
      if ($("fPeer")) $("fPeer").checked = true;
      if ($("fBlocks")) $("fBlocks").checked = true;
      if ($("fTip")) $("fTip").checked = true;
      if (lastSnap) renderEventLog(lastSnap);
    });
  }
}

function setupGraphHover() {
  const canvas = $("graph");
  const tip = $("tooltip");
  if (!canvas || !tip) return;

  const hide = () => {
    tip.classList.add("hidden");
    window.__hoverEdgeKey = null;
    if (lastSnap) drawGraph(lastSnap);
  };

  canvas.addEventListener("mouseleave", hide);
  canvas.addEventListener("mousemove", (ev) => {
    const rect = canvas.getBoundingClientRect();
    const x = ev.clientX - rect.left;
    const y = ev.clientY - rect.top;

    let best = null;
    let bestD = 9999;
    for (const e of graphHit.edges || []) {
      const d = distToSegment(x, y, e.ax, e.ay, e.bx, e.by);
      if (d < bestD) {
        bestD = d;
        best = e;
      }
    }

    if (!best || bestD > 10) {
      if (!tip.classList.contains("hidden")) hide();
      return;
    }

    window.__hoverEdgeKey = best.key;
    if (lastSnap) drawGraph(lastSnap);

    const title = `${best.fromId} → ${best.toId} • ${best.up ? "UP" : "DOWN"}`;
    const sub = `${best.fromAddr} → ${best.toAddr} • last ${fmtTime(best.lastAt)} (${fmtAbsTime(best.lastAt)})`;
    tip.innerHTML = `<div class="tTitle">${esc(title)}</div><div class="tSub">${esc(sub)}</div>`;
    tip.style.left = `${ev.clientX + 14}px`;
    tip.style.top = `${ev.clientY + 14}px`;
    tip.classList.remove("hidden");
  });
}

async function boot() {
  setupLogControls();
  setupGraphHover();

  try {
    const res = await fetch("/api/state");
    const snap = await res.json();
    renderAll(snap);
  } catch (e) {
    $("footerStatus").textContent = "Failed to load initial state";
  }

  const es = new EventSource("/events");
  es.onopen = () => {
    $("footerStatus").textContent = "Connected";
  };
  es.onerror = () => {
    $("footerStatus").textContent = "Reconnecting…";
  };
  es.addEventListener("snapshot", (msg) => {
    try {
      const snap = JSON.parse(msg.data);
      renderAll(snap);
    } catch (e) { }
  });
}

boot();

