/* 21-chart.js - the traffic chart: allowed and denied decisions per
   half-second of simulated time over the last 30 seconds, with the level the
   policy can sustain and a mark wherever the policy was changed.            */

var chartHover = -2; // absolute bin index under the pointer (-2 = none)

function chartGeom(w, h) {
  return { x0: 44, x1: w - 14, y0: 16, y1: h - 26 };
}

// The level the current policy lets through, in requests per half-second.
function sustainPerBin() {
  var eff = cfg.limit;
  if (cfg.algo === "adaptive") {
    var st = sim.store.adaptive.states.get(focusKey());
    if (st) eff = Math.trunc(st.limit);
  }
  return C.sustainOf(cfg, eff) * 0.5;
}

function drawChart() {
  var canvas = el("chart");
  var f = fitCanvas(canvas);
  if (!f) return;
  var ctx = f.ctx;
  var G = chartGeom(f.w, f.h);
  var pw = G.x1 - G.x0;
  var ph = G.y1 - G.y0;
  var a;
  var b;
  var i;

  ctx.clearRect(0, 0, f.w, f.h);

  var now = Number(sim.t / NS_PER_S);
  var leftT = Math.max(0, now - 30);
  var span = Math.max(30, now) - leftT;
  var X = function (t) {
    return G.x0 + ((t - leftT) / span) * pw;
  };
  var lo = Math.max(0, sim.binAbs - 59);
  var hi = Math.max(-1, sim.binAbs);

  var sustain = sustainPerBin();
  var maxTot = hasKey() ? sustain : 0;
  for (a = lo; a <= hi; a++) {
    b = sim.bins[a % BIN_COUNT];
    if (b.ok + b.no > maxTot) maxTot = b.ok + b.no;
  }
  var yMax = niceMax(maxTot);
  var Y = function (v) {
    return G.y1 - (v / yMax) * ph;
  };

  // grid and y labels
  ctx.font = monoFont(11);
  ctx.textBaseline = "middle";
  ctx.textAlign = "right";
  var levels = [
    [yMax, "rgba(148,163,184,0.12)"],
    [yMax / 2, "rgba(148,163,184,0.08)"],
    [0, "rgba(148,163,184,0.3)"],
  ];
  for (i = 0; i < levels.length; i++) {
    var ly = Math.round(Y(levels[i][0])) + 0.5;
    ctx.fillStyle = levels[i][1];
    ctx.fillRect(G.x0, ly, pw, 1);
    ctx.fillStyle = COL.dim;
    ctx.fillText(C.fmtNum(levels[i][0]), G.x0 - 8, clamp(ly, 8, G.y1 + 8));
  }

  // policy changes
  for (i = 0; i < sim.markers.length; i++) {
    var mt = Number(sim.markers[i] / NS_PER_S);
    if (mt < leftT || mt > now) continue;
    var mx = Math.round(X(mt)) + 0.5;
    ctx.fillStyle = "rgba(34,211,238,0.5)";
    ctx.fillRect(mx, G.y0, 1, ph);
  }

  // hovered half-second
  var pitch = (pw / span) * 0.5;
  if (chartHover >= lo && chartHover <= hi) {
    ctx.fillStyle = "rgba(148,163,184,0.09)";
    ctx.fillRect(Math.round(X(chartHover * 0.5)), G.y0, Math.ceil(pitch), ph);
  }

  // stacked bars
  var barW = Math.max(1.5, pitch - clamp(pitch * 0.25, 1, 2.5));
  for (a = Math.max(0, Math.floor(leftT * 2)); a <= hi; a++) {
    b = sim.bins[a % BIN_COUNT];
    var bx = X(a * 0.5);
    if (bx + barW < G.x0 || bx > G.x1) continue;
    var bw2 = Math.min(barW, G.x1 - bx); // the live bin must not pass the frame
    var okH = (b.ok / yMax) * ph;
    var noH = (b.no / yMax) * ph;
    if (b.ok) {
      ctx.fillStyle = COL.ok;
      ctx.fillRect(bx, G.y1 - okH, bw2, Math.max(1, okH));
    }
    if (b.no) {
      ctx.fillStyle = COL.no;
      ctx.fillRect(bx, G.y1 - okH - noH, bw2, Math.max(1, noH));
    }
  }

  // what the policy sustains
  var sy = Math.round(Y(sustain)) + 0.5;
  ctx.save();
  ctx.setLineDash([5, 4]);
  ctx.strokeStyle = COL.cyan;
  ctx.lineWidth = 1.5;
  ctx.beginPath();
  ctx.moveTo(G.x0, sy);
  ctx.lineTo(G.x1, sy);
  ctx.stroke();
  ctx.restore();
  ctx.fillStyle = COL.cyan;
  ctx.textAlign = "right";
  ctx.font = monoFont(11, 600);
  ctx.fillText(C.fmtNum(sustain * 2) + " req/s", G.x1, sy - 9 >= G.y0 + 4 ? sy - 9 : sy + 10);

  // x labels
  ctx.fillStyle = COL.dim;
  ctx.font = monoFont(11);
  ctx.textAlign = "center";
  for (var t = Math.ceil(leftT / 10) * 10; t <= now - 3; t += 10) {
    var lbl = t === 0 ? "start" : "-" + Math.round(now - t) + " s";
    ctx.fillText(lbl, X(t), G.y1 + 14);
  }
  ctx.textAlign = "right";
  ctx.fillText("now", G.x1, G.y1 + 14);

  updateTip(G, X, Y, lo, hi, now);
}

// Tooltip over the hovered bar: position is re-evaluated every frame because
// the bar's height changes while the simulation runs.
function updateTip(G, X, Y, lo, hi, now) {
  var tip = el("chartTip");
  if (!(chartHover >= lo && chartHover <= hi)) {
    if (tip.__on) {
      tip.classList.remove("show");
      tip.__on = false;
    }
    return;
  }
  var b = sim.bins[chartHover % BIN_COUNT];
  var total = b.ok + b.no;
  var when = now - (chartHover + 0.5) * 0.5;
  var head = when <= 0.5 ? "this half-second" : C.fmtNum(Math.round(when * 10) / 10) + " s ago";
  var key = head + "|" + b.ok + "|" + b.no;
  if (tip.__k !== key) {
    tip.__k = key;
    tip.replaceChildren(
      node("b", head),
      node("span", "allowed " + C.fmtInt(b.ok), "ok"),
      node("span", "denied " + C.fmtInt(b.no), "no"),
      node("span", "total " + C.fmtInt(total))
    );
  }
  if (!tip.__on) {
    tip.classList.add("show");
    tip.__on = true;
  }
  var canvas = el("chart");
  var cardW = canvas.offsetParent ? canvas.offsetParent.clientWidth : G.x1 + 14;
  var tw = tip.offsetWidth;
  var th = tip.offsetHeight;
  var cx = canvas.offsetLeft + clamp(X(chartHover * 0.5) + 2, G.x0, G.x1);
  var top = Y(b.ok + b.no);
  var left = clamp(cx - tw / 2, 8, Math.max(8, cardW - tw - 8));
  var y = canvas.offsetTop + top - th - 10;
  if (y < canvas.offsetTop - 30) y = canvas.offsetTop + top + 12;
  tip.style.left = Math.round(left) + "px";
  tip.style.top = Math.round(y) + "px";
}

// Pointer -> absolute bin index.
function chartPointer(e) {
  var canvas = el("chart");
  var rect = canvas.getBoundingClientRect();
  var G = chartGeom(rect.width, rect.height);
  var px = e.clientX - rect.left;
  if (px < G.x0 - 3 || px > G.x1 + 3) {
    chartHover = -2;
    return;
  }
  var now = Number(sim.t / NS_PER_S);
  var leftT = Math.max(0, now - 30);
  var span = Math.max(30, now) - leftT;
  var t = leftT + ((px - G.x0) / (G.x1 - G.x0)) * span;
  chartHover = Math.floor(t * 2);
}

function wireChartPointer() {
  var canvas = el("chart");
  canvas.addEventListener("pointermove", chartPointer);
  canvas.addEventListener("pointerdown", chartPointer);
  canvas.addEventListener("pointerleave", function () {
    chartHover = -2;
  });
  canvas.addEventListener("pointercancel", function () {
    chartHover = -2;
  });
}
