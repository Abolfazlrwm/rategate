/* 20-viz.js - the selected key's state, drawn per algorithm:
   token bucket  -> a liquid vessel with the token level, refill droplets and
                    a 10 s history of the projected token count
   sliding window-> the real 60-slot ring, oldest bucket on the left
   adaptive AIMD -> one column per window (admitted / rejected) with the
                    stepped ceiling between the floor and the cap                */

var vizFx = { wave: 0, tick: 0, drops: [], splashes: [], dropDebt: 0 };
var vizCard = null;

// Zones are shared by the three pictures so the DOM overlay (big number on
// top, two captions at the bottom) never sits on top of the graphics.
var VZ = { top: 54, base: 128 };

function dashLine(ctx, x0, y, x1) {
  ctx.save();
  ctx.setLineDash([4, 4]);
  ctx.beginPath();
  ctx.moveTo(x0, y + 0.5);
  ctx.lineTo(x1, y + 0.5);
  ctx.stroke();
  ctx.restore();
}

// ------------------------------------------------------------- token bucket

function bucketGeom(w) {
  var wide = w >= 430; // narrow screens drop the history and the scale
  return { wide: wide, x0: 180, x1: wide ? 276 : w - 20, y0: 14, y1: 154, sx0: 316, sx1: w - 16 };
}

function drawBucket(ctx, G, dt, tokens, playing) {
  var limit = cfg.limit;
  var frac = limit > 0 ? clamp(tokens / limit, 0, 1) : 0;
  var x0 = G.x0;
  var x1 = G.x1;
  var y0 = G.y0;
  var y1 = G.y1;
  var bw = x1 - x0;
  var bh = y1 - y0;
  var level = y1 - 5 - frac * (bh - 10);
  var i;
  var x;
  var sy;

  if (!reduceMotion && playing) vizFx.wave += dt;
  var amp = 1.2 + 2.2 * (1 - frac);

  ctx.fillStyle = "rgba(7,11,22,0.65)";
  rr(ctx, x0, y0, bw, bh, 12);
  ctx.fill();

  ctx.save();
  rr(ctx, x0 + 1.5, y0 + 1.5, bw - 3, bh - 3, 10.5);
  ctx.clip();

  // liquid
  var g = safeGrad(ctx, 0, Math.max(y0, level - 10), 0, y1);
  g.addColorStop(0, "rgba(34,211,238,0.7)");
  g.addColorStop(1, "rgba(167,139,250,0.55)");
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.moveTo(x0, y1 + 2);
  ctx.lineTo(x0, level);
  for (x = x0; x <= x1; x += 4) {
    sy = level + Math.sin(x * 0.11 + vizFx.wave * 2.4) * amp + Math.sin(x * 0.053 - vizFx.wave * 1.5) * 1.2;
    ctx.lineTo(x, sy);
  }
  ctx.lineTo(x1, y1 + 2);
  ctx.closePath();
  ctx.fill();

  // surface highlight
  ctx.strokeStyle = "rgba(226,232,240,0.45)";
  ctx.lineWidth = 1;
  ctx.beginPath();
  for (x = x0; x <= x1; x += 4) {
    sy = level + Math.sin(x * 0.11 + vizFx.wave * 2.4) * amp + Math.sin(x * 0.053 - vizFx.wave * 1.5) * 1.2;
    if (x === x0) ctx.moveTo(x, sy);
    else ctx.lineTo(x, sy);
  }
  ctx.stroke();

  // refill: one droplet per token (capped so a fast refill stays readable)
  if (!reduceMotion && playing && tokens < limit - 1e-6) {
    vizFx.dropDebt = Math.min(4, vizFx.dropDebt + dt * Math.min(14, cfg.refill));
    while (vizFx.dropDebt >= 1 && vizFx.drops.length < 20) {
      vizFx.dropDebt -= 1;
      vizFx.drops.push({ x: x0 + 9 + Math.random() * (bw - 18), y: y0 - 2, v: 150 + Math.random() * 80 });
    }
  } else if (!playing) {
    vizFx.dropDebt = 0;
  }
  ctx.fillStyle = "rgba(34,211,238,0.9)";
  for (i = vizFx.drops.length - 1; i >= 0; i--) {
    var d = vizFx.drops[i];
    if (playing) d.y += d.v * dt;
    if (d.y >= level) {
      vizFx.drops.splice(i, 1);
      if (!reduceMotion) vizFx.splashes.push({ x: d.x, t0: vizFx.tick });
      continue;
    }
    ctx.beginPath();
    ctx.ellipse(d.x, d.y, 1.6, 3.8, 0, 0, 6.2832);
    ctx.fill();
  }
  for (i = vizFx.splashes.length - 1; i >= 0; i--) {
    var s = vizFx.splashes[i];
    var age = vizFx.tick - s.t0;
    if (age > 0.35) {
      vizFx.splashes.splice(i, 1);
      continue;
    }
    var pr = age / 0.35;
    ctx.strokeStyle = "rgba(226,232,240," + ((1 - pr) * 0.5).toFixed(3) + ")";
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.ellipse(s.x, level, 2 + pr * 7, 1 + pr * 2, 0, 0, 6.2832);
    ctx.stroke();
  }
  ctx.restore();

  ctx.strokeStyle = "rgba(148,163,184,0.45)";
  ctx.lineWidth = 1.5;
  rr(ctx, x0, y0, bw, bh, 12);
  ctx.stroke();

  // scale
  if (G.wide) {
    ctx.font = monoFont(11);
    ctx.fillStyle = COL.dim;
    ctx.textAlign = "left";
    ctx.textBaseline = "middle";
    var vals = [limit, limit / 2, 0];
    var ys = [y0 + 7, (y0 + y1) / 2, y1 - 7];
    ctx.fillStyle = "rgba(148,163,184,0.4)";
    for (i = 0; i < 3; i++) ctx.fillRect(x1 + 1, ys[i] - 0.5, 4, 1);
    ctx.fillStyle = COL.dim;
    for (i = 0; i < 3; i++) ctx.fillText(C.fmtNum(vals[i]), x1 + 8, ys[i]);
  }
}

function drawSpark(ctx, G) {
  var x0 = G.sx0;
  var x1 = G.sx1;
  if (x1 - x0 < 50) return;
  var yt = 26;
  var yb = 142;
  var limit = cfg.limit;
  var hist = sim.tokHist;
  var n = hist.length;
  var i;
  var px = function (i) {
    return x0 + (i / (n - 1)) * (x1 - x0);
  };
  var py = function (v) {
    return limit > 0 ? yb - (clamp(v, 0, limit) / limit) * (yb - yt) : yb;
  };

  ctx.font = monoFont(11);
  ctx.textBaseline = "middle";
  ctx.fillStyle = COL.dim;
  ctx.textAlign = "left";
  ctx.fillText("tokens", x0, 12);
  ctx.fillText("0", x0, 155);
  ctx.textAlign = "right";
  ctx.fillText("last 10 s", x1, 12);
  ctx.fillText("now", x1, 155);

  ctx.strokeStyle = "rgba(148,163,184,0.22)";
  ctx.lineWidth = 1;
  dashLine(ctx, x0, yt, x1);
  ctx.beginPath();
  ctx.moveTo(x0, yb + 0.5);
  ctx.lineTo(x1, yb + 0.5);
  ctx.stroke();

  var start = -1;
  for (i = 0; i < n; i++) {
    if (hist[i] != null) {
      start = i;
      break;
    }
  }
  if (start < 0) return;
  var last = py(hist[start]);
  ctx.beginPath();
  ctx.moveTo(px(start), last);
  for (i = start + 1; i < n; i++) {
    var v = hist[i] == null ? hist[i - 1] : hist[i];
    last = py(v);
    ctx.lineTo(px(i), last);
  }
  var fill = safeGrad(ctx, 0, yt, 0, yb);
  fill.addColorStop(0, "rgba(34,211,238,0.32)");
  fill.addColorStop(1, "rgba(34,211,238,0.02)");
  ctx.fillStyle = fill;
  ctx.lineTo(x1, yb);
  ctx.lineTo(px(start), yb);
  ctx.closePath();
  ctx.fill();

  ctx.strokeStyle = COL.cyan;
  ctx.lineWidth = 1.5;
  ctx.lineJoin = "round";
  ctx.beginPath();
  ctx.moveTo(px(start), py(hist[start]));
  for (i = start + 1; i < n; i++) {
    ctx.lineTo(px(i), py(hist[i] == null ? hist[i - 1] : hist[i]));
  }
  ctx.stroke();

  // marker on the newest sample
  ctx.fillStyle = "#f1f5f9";
  ctx.beginPath();
  ctx.arc(x1, py(hist[n - 1] == null ? limit : hist[n - 1]), 2.5, 0, 6.2832);
  ctx.fill();
}

// --------------------------------------------------------- sliding window

function drawWindowStrip(ctx, w, view) {
  var x0 = 16;
  var x1 = w - 16;
  var yb = VZ.base;
  var yt = VZ.top;
  var H = yb - yt;
  var g = view.g;
  var pitch = (x1 - x0) / g;
  var bw = Math.max(2, pitch - 1.5);
  var limit = cfg.limit;
  var i;
  var sx;

  ctx.fillStyle = "rgba(148,163,184,0.07)";
  for (i = 0; i < g; i++) {
    sx = x0 + i * pitch + (pitch - bw) / 2;
    ctx.fillRect(sx, yt, bw, H);
  }

  var grad = safeGrad(ctx, 0, yb, 0, yt);
  grad.addColorStop(0, "rgba(34,211,238,0.95)");
  grad.addColorStop(1, "rgba(167,139,250,0.95)");
  ctx.fillStyle = grad;
  for (i = 0; i < g; i++) {
    var s = view.slots[i];
    if (!s.live || !s.count) continue;
    var hh = limit > 0 ? Math.max(1.5, (s.count / limit) * H) : 1.5;
    ctx.fillRect(x0 + i * pitch + (pitch - bw) / 2, yb - hh, bw, hh);
  }

  // the slot the next admitted request is written to (always the newest)
  var wx = x0 + (g - 1) * pitch + (pitch - bw) / 2;
  ctx.fillStyle = "rgba(241,245,249,0.8)";
  ctx.fillRect(wx - 0.5, yt - 6, bw + 1, 2);

  ctx.fillStyle = "rgba(148,163,184,0.25)";
  ctx.fillRect(x0, yb, x1 - x0, 1);
  ctx.fillStyle = "rgba(148,163,184,0.35)";
  for (i = 0; i <= g; i += 10) ctx.fillRect(x0 + i * pitch - 0.5, yb + 1, 1, 4);

  if (w >= 430) {
    ctx.font = monoFont(11);
    ctx.fillStyle = COL.dim;
    ctx.textAlign = "right";
    ctx.textBaseline = "middle";
    ctx.fillText("admitted requests per " + C.fmtDur(view.bucketDur) + " bucket, oldest to newest", x1, 30);
  }
}

// ---------------------------------------------------------- adaptive AIMD

function drawAimd(ctx, w, view, dt) {
  var x0 = 60;
  var x1 = w - 16;
  var yb = VZ.base;
  var yt = VZ.top;
  var H = yb - yt;
  var floor = view.floor;
  var cap = view.cap;
  var ys = function (n) {
    return yb - (cap > 0 ? clamp(n, 0, cap) / cap : 0) * H;
  };
  var i;

  if (!reduceMotion) vizFx.tick += dt;

  ctx.font = monoFont(11);
  ctx.textBaseline = "middle";
  ctx.textAlign = "right";
  ctx.strokeStyle = "rgba(148,163,184,0.28)";
  ctx.lineWidth = 1;
  dashLine(ctx, x0, yt, x1);
  dashLine(ctx, x0, ys(floor), x1);
  ctx.fillStyle = COL.dim;
  ctx.fillText("cap " + cap, x0 - 8, yt);
  ctx.fillText("floor " + floor, x0 - 8, ys(floor));

  var cols = [];
  var hist = view.hist;
  var from = Math.max(0, hist.length - 14);
  for (i = from; i < hist.length; i++) {
    cols.push({ ok: hist[i].admitted, no: hist[i].rejected, limit: hist[i].limitBefore, open: false });
  }
  var st = view.state;
  if (st) cols.push({ ok: st.count, no: st.rejects, limit: Math.trunc(st.limit), open: true });

  var step = clamp(Math.floor((x1 - x0) / 15), 11, 30);
  var cw = step - 3;
  var n = cols.length;
  for (i = n - 1; i >= 0; i--) {
    var c = cols[i];
    var k = n - 1 - i; // 0 = newest, anchored at the right edge
    var cx = x1 - (k + 1) * step;
    if (cx < x0) break;
    c.x = cx;
    var ha = cap > 0 ? (c.ok / cap) * H : 0;
    var hn = cap > 0 ? (c.no / cap) * H : 0;
    ctx.globalAlpha = c.open ? 0.82 : 1;
    ctx.fillStyle = COL.ok;
    ctx.fillRect(cx, yb - ha, cw, Math.max(c.ok ? 1.5 : 0, ha));
    ctx.fillStyle = COL.no;
    ctx.fillRect(cx, yb - ha - hn, cw, Math.max(c.no ? 1.5 : 0, hn));
    ctx.globalAlpha = 1;
  }

  // the ceiling in force during each window, as a step line
  ctx.strokeStyle = COL.violet;
  ctx.lineWidth = 2;
  ctx.lineJoin = "round";
  ctx.beginPath();
  var started = false;
  for (i = 0; i < n; i++) {
    c = cols[i];
    if (c.x === undefined) continue;
    var ly = ys(c.limit);
    if (!started) {
      ctx.moveTo(c.x, ly);
      started = true;
    } else {
      ctx.lineTo(c.x, ly);
    }
    ctx.lineTo(c.x + cw, ly);
  }
  if (started) ctx.stroke();

  // the open window: dashed outline, gently pulsing
  var cur = cols[n - 1];
  if (cur && cur.x !== undefined) {
    var pulse = reduceMotion ? 0.6 : 0.45 + 0.3 * Math.sin(vizFx.tick * 4);
    ctx.strokeStyle = "rgba(241,245,249," + pulse.toFixed(3) + ")";
    ctx.lineWidth = 1;
    ctx.setLineDash([3, 3]);
    ctx.strokeRect(cur.x - 1.5, yt - 4, cw + 3, H + 4);
    ctx.setLineDash([]);
  }

  ctx.fillStyle = "rgba(148,163,184,0.25)";
  ctx.fillRect(x0, yb, x1 - x0, 1);

  if (!n) {
    ctx.font = "500 12px " + FONT;
    ctx.fillStyle = COL.dim;
    ctx.textAlign = "center";
    ctx.fillText("The first " + C.fmtWindow(C.windowMsOf(cfg)) + " window is still open", (x0 + x1) / 2, yt + 18);
  }
}

// ----------------------------------------------------------------- driver

function drawViz(dt) {
  var f = fitCanvas(el("viz"));
  if (!f) return;
  var ctx = f.ctx;
  var key = focusKey();
  ctx.clearRect(0, 0, f.w, f.h);
  if (cfg.algo === "tokenbucket") {
    var G = bucketGeom(f.w);
    var tokens = hasKey() ? projectedTokens(key, sim.t) : cfg.limit;
    drawBucket(ctx, G, dt, tokens, sim.playing);
    drawSpark(ctx, G);
  } else if (cfg.algo === "slidingwindow") {
    drawWindowStrip(ctx, f.w, windowView(key, sim.t));
  } else {
    drawAimd(ctx, f.w, adaptiveView(key), dt);
  }
}
