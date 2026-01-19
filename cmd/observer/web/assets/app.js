const $ = (id) => document.getElementById(id);

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

  // Edges
  ctx.lineWidth = 1;
  edges.forEach((e) => {
    const a = pos.get(e.fromAddr);
    const b = pos.get(e.toAddr);
    if (!a || !b) return;
    ctx.beginPath();
    ctx.moveTo(a.x, a.y);
    ctx.lineTo(b.x, b.y);
    ctx.strokeStyle = e.up ? "rgba(86,166,255,.55)" : "rgba(255,255,255,.12)";
    ctx.stroke();
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
  renderStats(snap);
  renderNodes(snap);
  drawGraph(snap);
  $("footerStatus").textContent = `Live • ${new Date(snap.at || Date.now()).toLocaleTimeString()}`;
}

async function boot() {
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

