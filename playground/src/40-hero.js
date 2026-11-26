/* 40-hero.js - the live gate in the hero card: a real token bucket
   (burst 20, refill 12 tokens a second) under a wave of traffic that peaks
   above what the bucket can pay for, so denials happen on their own.        */

var HERO = { limit: 20, refill: 12, mean: 17, period: 9, flightNs: 600000000n, max: 60 };
var heroPolicy = { limit: HERO.limit, refillPerSecond: HERO.refill, windowNs: 0n };
var hero = { bucket: new A.TokenBucket(), ok: 0, no: 0, t0: performance.now() };

function heroSec() {
  return (performance.now() - hero.t0) / 1000;
}

function heroNs() {
  return BigInt(Math.round(heroSec() * 1e9));
}

// 17 req/s on average, swinging between 3.4 and 30.6 with a 9 s period
function heroRate() {
  return HERO.mean * (1 + 0.8 * Math.sin((2 * Math.PI * heroSec()) / HERO.period));
}

function heroDecide(key, tNs) {
  var d = hero.bucket.check(key, heroPolicy, tNs).decision;
  if (d.allowed) hero.ok++;
  else hero.no++;
  setText(el("heroAllowed"), C.fmtInt(hero.ok));
  setText(el("heroDenied"), C.fmtInt(hero.no));
  return d;
}

function heroStep(dt) {
  var n = C.poisson(Math.random, heroRate() * dt);
  if (!n) return;
  var now = heroNs();
  var span = BigInt(Math.round(dt * 1e9));
  for (var i = 0; i < n; i++) {
    var born = now - span + BigInt(Math.floor(Math.random() * Number(span)));
    if (born < 0n) born = 0n;
    if (reduceMotion || heroField.particles.length >= HERO.max) heroDecide("visitor", born);
    else heroField.particles.push({ k: "visitor", born: born, arrive: born + HERO.flightNs, y0: Math.random(), w: Math.random() * 6.2832 });
  }
  heroField.settle();
}

function heroBurst(n) {
  var now = heroNs();
  if (reduceMotion || heroField.particles.length + n > HERO.max) {
    var oks = 0;
    for (var j = 0; j < n; j++) if (heroDecide("visitor", now).allowed) oks++;
    heroField.streak(oks, n - oks);
  } else {
    for (var i = 0; i < n; i++) {
      heroField.particles.push({
        k: "visitor",
        born: now,
        arrive: now + HERO.flightNs + BigInt(Math.round(i * 0.02 * 1e9)),
        y0: 0.12 + (0.76 * (i % 9)) / 8,
        w: i * 1.3,
      });
    }
  }
  heroField.settle();
}

// Token gauge under the gate: the same lazy projection the next Check would use.
function heroGauge(ctx, L) {
  var b = hero.bucket.buckets.get("visitor");
  var tokens = HERO.limit;
  if (b) tokens = Math.min(HERO.limit, b.tokens + A.secondsOf(heroNs() - b.lastFillNs) * HERO.refill);
  var w = 132;
  var x = L.gx - w / 2;
  var y = L.h - 30;
  ctx.font = monoFont(11);
  ctx.textBaseline = "middle";
  ctx.fillStyle = "rgba(148,163,184,0.18)";
  rr(ctx, x, y, w, 6, 3);
  ctx.fill();
  var g = safeGrad(ctx, x, 0, x + w, 0);
  g.addColorStop(0, COL.cyan);
  g.addColorStop(1, COL.violet);
  ctx.fillStyle = g;
  rr(ctx, x, y, Math.max(6, (clamp(tokens, 0, HERO.limit) / HERO.limit) * w), 6, 3);
  ctx.fill();
  ctx.fillStyle = COL.dim;
  ctx.textAlign = "right";
  ctx.fillText("bucket", x - 10, y + 3);
  ctx.fillStyle = COL.text2;
  ctx.textAlign = "left";
  ctx.fillText(tokText(tokens, HERO.limit) + " / " + HERO.limit + " tokens", x + w + 10, y + 3);
}
