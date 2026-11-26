/* 10-flow.js - the particle "gate" field shared by the hero card and the stage.
   Travel is measured in the owner's clock (simulated time on the stage, real
   time in the hero), so pausing the simulation freezes requests mid-flight,
   while the admit/deny effects run on the wall clock. */

var MONO = '"JetBrains Mono", ui-monospace, Menlo, Consolas, monospace';
var FONT = '"Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';

// CSS-pixel size and a DPR-aware context. Sizes are cached and only re-measured
// when a ResizeObserver reports a change, so no layout is forced per frame.
function fitCanvas(canvas) {
  if (canvas.__dirty !== false) {
    var rect = canvas.getBoundingClientRect();
    canvas.__fit = isFinite(rect.width) && isFinite(rect.height) && rect.width >= 2 && rect.height >= 2 ? { w: Math.round(rect.width), h: Math.round(rect.height) } : null;
    canvas.__dirty = false;
  }
  var fit = canvas.__fit;
  if (!fit) return null;
  var dpr = clamp(window.devicePixelRatio || 1, 1, 2);
  var bw = Math.round(fit.w * dpr);
  var bh = Math.round(fit.h * dpr);
  if (canvas.width !== bw || canvas.height !== bh) {
    canvas.width = bw;
    canvas.height = bh;
  }
  var ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  fit.ctx = ctx;
  fit.dpr = dpr;
  return fit;
}

function markCanvasesDirty() {
  var all = document.querySelectorAll("canvas");
  for (var i = 0; i < all.length; i++) all[i].__dirty = true;
}

function rr(ctx, x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function monoFont(size, weight) {
  return (weight || 500) + " " + size + "px " + MONO;
}

// opts: nowNs() -> BigInt, decide(key, tNs) -> decision, rate() -> req/s,
//       moving() -> bool, layout "stage" | "hero"
function Field(canvas, opts) {
  this.canvas = canvas;
  this.opts = opts;
  this.particles = []; // {k, born, arrive, y0, w}
  this.fx = []; // admit / deny effects
  this.ripples = [];
  this.flash = { ok: 0, no: 0 };
  this.flow = 0; // phase of the dashes on the load beam
  this.gate = { w: 0, h: 0, top: 0, bot: 0, gx: 0, gy: 0, size: 40 };
  this.lastRipple = -1;
}

Field.prototype.now = function () {
  return performance.now() / 1000;
};

Field.prototype.layoutOf = function (w, h) {
  var hero = this.opts.layout === "hero";
  var top = h * (hero ? 0.22 : 0.24);
  var bot = h * (hero ? 0.74 : 0.78);
  return {
    w: w,
    h: h,
    top: top,
    bot: bot,
    gx: w * (hero ? 0.5 : 0.56),
    gy: (top + bot) / 2,
    size: clamp(Math.min(w, h) * (hero ? 0.3 : 0.36), 34, 88),
  };
};

// Decide every request that has reached the gate by now.
Field.prototype.settle = function () {
  var now = this.opts.nowNs();
  var ps = this.particles;
  for (var i = ps.length - 1; i >= 0; i--) {
    var p = ps[i];
    if (p.arrive > now) continue;
    ps.splice(i, 1);
    this.land(p, !!this.opts.decide(p.k, p.arrive).allowed);
  }
};

Field.prototype.land = function (p, ok) {
  var t = this.now();
  var g = this.gate;
  this.flash[ok ? "ok" : "no"] += 1;
  if (t - this.lastRipple > 0.07) {
    this.lastRipple = t;
    this.ripples.push({ t0: t, ok: ok });
  }
  this.fx.push({
    t0: t,
    ok: ok,
    x0: g.gx - g.size / 2 - 3,
    y0: p.y0,
    w: p.w || 0,
    dir: Math.sin(p.w * 3.1) > 0 ? 1 : -1,
    life: ok ? 0.6 : 0.5,
  });
  if (this.fx.length > 140) this.fx.shift();
};

// Requests decided without a particle (paused simulation or reduced motion):
// show them as streaks that cross the whole field.
Field.prototype.streak = function (oks, nos) {
  var t = this.now();
  var n = oks + nos;
  this.flash.ok += oks;
  this.flash.no += nos;
  this.ripples.push({ t0: t, ok: oks >= nos });
  for (var i = 0; i < n; i++) {
    this.fx.push({
      t0: t + i * 0.04,
      ok: i < oks,
      x0: 0,
      y0: 0.2 + 0.6 * ((i * 0.382) % 1),
      w: i * 0.9,
      dir: 1,
      life: 0.42,
      streak: true,
    });
  }
  if (this.fx.length > 140) this.fx.splice(0, this.fx.length - 140);
};

Field.prototype.clear = function () {
  this.particles.length = 0;
  this.fx.length = 0;
  this.ripples.length = 0;
  this.flash.ok = 0;
  this.flash.no = 0;
};

Field.prototype.busy = function () {
  return this.particles.length + this.fx.length + this.ripples.length > 0 || this.flash.ok > 0.02 || this.flash.no > 0.02;
};

Field.prototype.drawBeam = function (ctx, L, dt) {
  var rate = this.opts.rate ? this.opts.rate() : 0;
  var a = clamp(0.06 + rate / 150, 0.06, 0.34);
  var x1 = L.gx - L.size / 2 - 6;
  if (x1 <= 0) return;
  var g = safeGrad(ctx, 0, 0, x1, 0);
  g.addColorStop(0, "rgba(34,211,238,0)");
  g.addColorStop(1, "rgba(34,211,238," + a.toFixed(3) + ")");
  ctx.fillStyle = g;
  ctx.fillRect(0, L.gy - L.size * 0.42, x1, L.size * 0.84);
  ctx.fillStyle = "rgba(34,211,238," + (a * 0.9).toFixed(3) + ")";
  ctx.fillRect(0, L.gy - 0.75, x1, 1.5);

  // dashes drifting toward the gate show direction and liveness
  if (reduceMotion) return;
  if (!this.opts.moving || this.opts.moving()) this.flow += dt * (34 + rate * 0.5);
  ctx.strokeStyle = "rgba(226,232,240," + Math.min(0.6, a * 1.7).toFixed(3) + ")";
  ctx.lineWidth = 1.5;
  ctx.lineCap = "round";
  ctx.beginPath();
  var step = 26;
  for (var x = -(this.flow % step); x < x1 - 8; x += step) {
    ctx.moveTo(x, L.gy);
    ctx.lineTo(x + 9, L.gy);
  }
  ctx.stroke();
};

Field.prototype.drawGate = function (ctx, L) {
  var s = L.size;
  var x = L.gx - s / 2;
  var y = L.gy - s / 2;
  var fo = Math.min(1, this.flash.ok * 0.2);
  var fn = Math.min(1, this.flash.no * 0.2);
  if (fo > 0.01) {
    ctx.lineWidth = 8;
    ctx.strokeStyle = "rgba(52,211,153," + (fo * 0.5).toFixed(3) + ")";
    rr(ctx, x - 4, y - 4, s + 8, s + 8, s * 0.2 + 5);
    ctx.stroke();
  }
  if (fn > 0.01) {
    ctx.lineWidth = 8;
    ctx.strokeStyle = "rgba(251,113,133," + (fn * 0.5).toFixed(3) + ")";
    rr(ctx, x - 4, y - 4, s + 8, s + 8, s * 0.2 + 5);
    ctx.stroke();
  }
  var g = safeGrad(ctx, x, y, x + s, y + s);
  g.addColorStop(0, COL.cyan);
  g.addColorStop(1, COL.violet);
  ctx.strokeStyle = g;
  ctx.lineWidth = 2;
  rr(ctx, x, y, s, s, s * 0.2);
  ctx.stroke();
  ctx.fillStyle = g;
  ctx.globalAlpha = 0.2;
  rr(ctx, x + s * 0.15, y + s * 0.15, s * 0.7, s * 0.7, s * 0.12);
  ctx.fill();
  ctx.globalAlpha = 1;
  ctx.lineWidth = Math.max(1.5, s * 0.045);
  ctx.lineCap = "round";
  ctx.beginPath();
  ctx.moveTo(x + s * 0.26, L.gy);
  ctx.lineTo(x + s * 0.74, L.gy);
  ctx.moveTo(L.gx, y + s * 0.26);
  ctx.lineTo(L.gx, y + s * 0.74);
  ctx.stroke();
  ctx.fillStyle = "#f8fafc";
  ctx.beginPath();
  ctx.arc(L.gx, L.gy, Math.max(2, s * 0.06), 0, 6.2832);
  ctx.fill();
};

Field.prototype.draw = function (dt) {
  var f = fitCanvas(this.canvas);
  if (!f) return;
  var ctx = f.ctx;
  var L = (this.gate = this.layoutOf(f.w, f.h));
  var nowNs = this.opts.nowNs();
  var t = this.now();
  var i;
  var p;
  var a;
  var x;
  var y;
  var al;
  var color;
  var tg;
  var trail;

  ctx.clearRect(0, 0, f.w, f.h);

  // decay the admit / deny glow
  var k = Math.exp(-dt * 5.5);
  this.flash.ok *= k;
  this.flash.no *= k;

  this.drawBeam(ctx, L, dt);
  this.drawGate(ctx, L);

  // requests in flight
  var xEnd = L.gx - L.size / 2 - 5;
  for (i = 0; i < this.particles.length; i++) {
    p = this.particles[i];
    var span = Number(p.arrive - p.born);
    if (span <= 0) continue;
    var u = clamp(Number(nowNs - p.born) / span, 0, 1);
    x = lerp(-6, xEnd, u);
    y = lerp(L.top, L.bot, p.y0) + Math.sin(p.w + u * 6) * 2.5;
    trail = 8 + 16 * u;
    tg = safeGrad(ctx, x - trail, y, x, y);
    tg.addColorStop(0, "rgba(226,232,240,0)");
    tg.addColorStop(1, "rgba(226,232,240,0.85)");
    ctx.strokeStyle = tg;
    ctx.lineWidth = 2;
    ctx.lineCap = "round";
    ctx.beginPath();
    ctx.moveTo(x - trail, y);
    ctx.lineTo(x, y);
    ctx.stroke();
    ctx.fillStyle = "rgba(34,211,238,0.18)";
    ctx.beginPath();
    ctx.arc(x, y, 6, 0, 6.2832);
    ctx.fill();
    ctx.fillStyle = "rgba(241,245,249,0.95)";
    ctx.beginPath();
    ctx.arc(x, y, 2.5, 0, 6.2832);
    ctx.fill();
  }

  // ripples on the gate frame
  for (i = this.ripples.length - 1; i >= 0; i--) {
    var r = this.ripples[i];
    a = t - r.t0;
    if (a > 0.6) {
      this.ripples.splice(i, 1);
      continue;
    }
    var pr = a / 0.6;
    var grow = pr * 22;
    ctx.lineWidth = 1.5;
    ctx.strokeStyle = (r.ok ? "rgba(52,211,153," : "rgba(251,113,133,") + ((1 - pr) * 0.5).toFixed(3) + ")";
    rr(ctx, L.gx - L.size / 2 - 4 - grow, L.gy - L.size / 2 - 4 - grow, L.size + 8 + grow * 2, L.size + 8 + grow * 2, L.size * 0.2 + 4 + pr * 10);
    ctx.stroke();
  }

  // admit (fly through) and deny (bounce back) effects
  for (i = this.fx.length - 1; i >= 0; i--) {
    var e = this.fx[i];
    a = t - e.t0;
    if (a < 0) continue;
    if (a >= e.life) {
      this.fx.splice(i, 1);
      continue;
    }
    y = lerp(L.top, L.bot, e.y0) + Math.sin(e.w * 1.3 + a * 9) * 2.5 + (e.ok ? 0 : e.dir * 14 * a);
    if (e.streak) {
      x = lerp(-6, L.w + 16, a / e.life);
      al = 1 - smooth(Math.max(0, (a - e.life * 0.55) / (e.life * 0.45)));
      trail = 22;
    } else if (e.ok) {
      x = e.x0 + (L.w + 16 - e.x0) * (a / e.life);
      al = 1 - smooth(clamp((a - e.life * 0.4) / (e.life * 0.6), 0, 1));
      trail = 18;
    } else {
      x = e.x0 - 34 * smooth(Math.min(1, a / 0.2));
      al = 1 - smooth(a / e.life);
      trail = 10;
    }
    color = e.ok ? "52,211,153" : "251,113,133";
    tg = safeGrad(ctx, e.ok ? x - trail : x + trail, y, x, y);
    tg.addColorStop(0, "rgba(" + color + ",0)");
    tg.addColorStop(1, "rgba(" + color + "," + (al * 0.8).toFixed(3) + ")");
    ctx.strokeStyle = tg;
    ctx.lineWidth = 2;
    ctx.lineCap = "round";
    ctx.beginPath();
    ctx.moveTo(e.ok ? x - trail : x + trail, y);
    ctx.lineTo(x, y);
    ctx.stroke();
    ctx.fillStyle = "rgba(" + color + "," + (al * 0.2).toFixed(3) + ")";
    ctx.beginPath();
    ctx.arc(x, y, 6, 0, 6.2832);
    ctx.fill();
    ctx.fillStyle = "rgba(" + color + "," + al.toFixed(3) + ")";
    ctx.beginPath();
    ctx.arc(x, y, 2.8, 0, 6.2832);
    ctx.fill();
  }
};
