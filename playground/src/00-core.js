/* 00-core.js - shared state, the simulated clock and the decision path.
   Concatenated into one IIFE by build.py together with 10..50 below. */

var A = globalThis.RateGateAlgorithms;
var C = globalThis.RateGateCore;

var NS_PER_MS = 1000000n;
var NS_PER_S = 1000000000n;
var BIN_NS = 500000000n; // traffic-chart bin: half a simulated second
var BIN_COUNT = 60; // ... 60 of them = the 30 s the chart shows
var FLIGHT = 0.55; // real seconds a request needs to travel to the gate
var MAX_PARTICLES = 90; // beyond this, requests are decided without a particle
var MAX_FEED = 48;
var MAX_ARRIVALS_PER_FRAME = 5000;

var COL = {
  ok: "#34d399",
  no: "#fb7185",
  cyan: "#22d3ee",
  violet: "#a78bfa",
  text: "#f1f5f9",
  text2: "#c3cddd",
  muted: "#94a3b8",
  dim: "#7c8ba3",
  grid: "rgba(148, 163, 184, 0.12)",
};

var mqReduce = window.matchMedia ? window.matchMedia("(prefers-reduced-motion: reduce)") : null;
var reduceMotion = !!(mqReduce && mqReduce.matches);

function $(id) {
  return document.getElementById(id);
}

function clamp(v, lo, hi) {
  return Math.min(hi, Math.max(lo, v));
}

function lerp(a, b, t) {
  return a + (b - a) * t;
}

function smooth(t) {
  return t * t * (3 - 2 * t);
}

// Linear gradient that never throws. createLinearGradient raises an
// IndexSizeError if any coordinate is non-finite (NaN/Infinity) — e.g. when a
// policy with limit=0 makes the token level NaN — and that exception would
// unwind straight out of the rAF loop and freeze the page. Fall back to a
// 1px gradient so addColorStop still works and the caller's fill is just a
// flat tint instead of crashing the whole animation.
function safeGrad(ctx, x0, y0, x1, y1) {
  if (!(isFinite(x0) && isFinite(y0) && isFinite(x1) && isFinite(y1))) {
    x0 = 0;
    y0 = 0;
    x1 = 1;
    y1 = 0;
  }
  return ctx.createLinearGradient(x0, y0, x1, y1);
}

// Round a chart maximum up to a 1/2/2.5/5 x 10^n step.
function niceMax(v) {
  if (!(v > 0)) return 4;
  var e = Math.pow(10, Math.floor(Math.log10(v)));
  var m = v / e;
  var step = m <= 1 ? 1 : m <= 2 ? 2 : m <= 2.5 ? 2.5 : m <= 5 ? 5 : 10;
  return step * e;
}

function setText(node, text) {
  if (node && node.__t !== text) {
    node.__t = text;
    node.textContent = text;
  }
}

function setPct(node, pct) {
  var v = clamp(pct, 0, 100).toFixed(1) + "%";
  if (node && node.__w !== v) {
    node.__w = v;
    node.style.width = v;
  }
}

function setPressed(group, attr, value) {
  var btns = group.querySelectorAll("[" + attr + "]");
  for (var i = 0; i < btns.length; i++) {
    btns[i].setAttribute("aria-pressed", btns[i].getAttribute(attr) === String(value) ? "true" : "false");
  }
}

// ------------------------------------------------------------ config

var shared = C.fromParams(window.location.search);
var cfg = C.normalize(shared || C.defaults());

var policyCache = null;
function policy() {
  if (!policyCache) policyCache = C.policyOf(cfg);
  return policyCache;
}

// Call after any cfg change that alters what the store is asked.
function invalidate() {
  policyCache = null;
}

// --------------------------------------------------------- simulation

function newBins() {
  var b = [];
  for (var i = 0; i < BIN_COUNT; i++) b.push({ ok: 0, no: 0 });
  return b;
}

var sim = {
  t: 0n, // simulated ns since the last full reset
  playing: true,
  store: new A.MemoryStore({ swGranularity: 60, adaptiveMaxFactor: C.MAX_FACTOR }),
  keys: C.deriveKeys(cfg.key, cfg.nKeys),
  focus: 0,
  skew: 0, // share of traffic sent to the focused key (scenario "noisy neighbour")
  ok: 0,
  no: 0,
  stats: new Map(), // key -> {ok, no, left}
  bins: newBins(),
  binAbs: -1, // absolute index of the newest bin
  markers: [], // sim.t values where the policy changed (drawn on the chart)
  last: null, // most recent decision for the focused key
  feed: [],
  aimd: new Map(), // key -> closed-window history
  tokHist: new Array(80).fill(null), // projected tokens of the focused key
  epochMs: Date.now(), // wall-clock base, so unix timestamps look real
  ghosts: 0,
  feedDirty: true,
  keysDirty: true,
};

function baseNs() {
  return BigInt(sim.epochMs) * NS_PER_MS;
}

function focusKey() {
  return sim.keys[Math.min(sim.focus, sim.keys.length - 1)] || "";
}

function hasKey() {
  return sim.keys.length > 0 && sim.keys[0] !== "";
}

// Forget every key's state but keep the clock and the chart, so a policy
// change is visible as a before/after on the same timeline.
function clearState(mark) {
  sim.store.reset();
  sim.stats.clear();
  sim.ok = 0;
  sim.no = 0;
  sim.last = null;
  sim.feed.length = 0;
  sim.aimd.clear();
  sim.tokHist.fill(null);
  if (stageField) stageField.particles.length = 0; // requests in flight belong to the old state
  if (mark) sim.markers.push(sim.t);
  sim.feedDirty = true;
  sim.keysDirty = true;
}

function resetAll() {
  clearState(false);
  sim.t = 0n;
  sim.bins = newBins();
  sim.binAbs = -1;
  sim.markers.length = 0;
  sim.epochMs = Date.now();
}

// One request reaches the gate at simulated time tRelNs (ns since reset).
function decide(key, tRelNs) {
  var res = sim.store.check(cfg.algo, key, policy(), baseNs() + tRelNs);
  var d = res.decision;
  var st = sim.stats.get(key);
  if (!st) {
    st = { ok: 0, no: 0, left: d.limit };
    sim.stats.set(key, st);
  }
  if (d.allowed) {
    st.ok++;
    sim.ok++;
  } else {
    st.no++;
    sim.no++;
  }
  st.left = d.remaining;

  var bin = sim.bins[Number(tRelNs / BIN_NS) % BIN_COUNT];
  if (d.allowed) bin.ok++;
  else bin.no++;

  sim.feed.unshift({ k: key, ok: d.allowed, left: d.remaining, t: tRelNs });
  if (sim.feed.length > MAX_FEED) sim.feed.pop();

  var ex = res.explain;
  if (cfg.algo === "adaptive" && ex && ex.closed) {
    var h = sim.aimd.get(key);
    if (!h) {
      h = [];
      sim.aimd.set(key, h);
    }
    h.push(ex.closed);
    if (h.length > 40) h.shift();
  }
  if (key === focusKey()) sim.last = { k: key, d: d, ex: ex, t: tRelNs, err: res.error };
  sim.feedDirty = true;
  return d;
}

function pickKey() {
  var ks = sim.keys;
  if (ks.length === 1) return ks[0];
  if (sim.skew > 0 && Math.random() < sim.skew) return ks[0];
  return ks[Math.min(ks.length - 1, 1 + Math.floor(Math.random() * (ks.length - 1)))];
}

function advanceBins(tNs) {
  var abs = Number(tNs / BIN_NS);
  if (sim.binAbs < 0) {
    sim.binAbs = abs;
    return;
  }
  while (sim.binAbs < abs) {
    sim.binAbs++;
    var b = sim.bins[sim.binAbs % BIN_COUNT];
    b.ok = 0;
    b.no = 0;
  }
}

// Advance the clock by dtReal real seconds and generate the arrivals.
function stepSim(dtReal) {
  var dtNs = BigInt(Math.round(dtReal * cfg.speed * 1e9));
  if (dtNs <= 0n) return;
  var t0 = sim.t;
  sim.t = t0 + dtNs;
  advanceBins(sim.t);
  if (!hasKey()) return;

  var sec = dtReal * cfg.speed;
  var lambda = cfg.rate * C.intensity(cfg.shape, Number(t0 / NS_PER_S)) * sec;
  var n = Math.min(MAX_ARRIVALS_PER_FRAME, C.poisson(Math.random, lambda));
  var flightNs = BigInt(Math.round(FLIGHT * cfg.speed * 1e9));
  var span = Number(dtNs);
  for (var i = 0; i < n; i++) {
    var key = pickKey();
    var born = t0 + BigInt(Math.floor(Math.random() * span));
    if (reduceMotion || stageField.particles.length >= MAX_PARTICLES) {
      decide(key, born);
      sim.ghosts++;
    } else {
      stageField.particles.push({ k: key, born: born, arrive: born + flightNs, y0: Math.random(), w: Math.random() * 6.283 });
    }
  }
  stageField.settle();
}

// Send n requests for the focused key right now (or as a tight volley).
function sendBurst(n) {
  if (!hasKey()) {
    toast("Type a rate-limit key first");
    return;
  }
  var key = focusKey();
  if (sim.playing && !reduceMotion) {
    for (var i = 0; i < n; i++) {
      var delay = BigInt(Math.round((i * 0.012 + Math.random() * 0.03) * cfg.speed * 1e9));
      stageField.particles.push({ k: key, born: sim.t, arrive: sim.t + BigInt(Math.round(FLIGHT * cfg.speed * 1e9)) + delay, y0: 0.2 + (0.6 * (i % 7)) / 6, w: i });
    }
  } else {
    // Time is frozen (or motion is reduced): decide right now and show the
    // outcome as streaks through the gate.
    var oks = 0;
    for (var j = 0; j < n; j++) if (decide(key, sim.t).allowed) oks++;
    stageField.streak(oks, n - oks);
  }
  stageField.settle();
}

// Projected token count for a key: what the next Check would see, because
// the port refills lazily from lastFillNs exactly like tokenbucket.go.
function projectedTokens(key, nowNs) {
  var b = sim.store.tokenBucket.buckets.get(key);
  if (!b) return cfg.limit; // a fresh key starts full
  var elapsed = nowNs - b.lastFillNs;
  var tokens = b.tokens + (elapsed > 0n ? A.secondsOf(elapsed) * cfg.refill : 0);
  return Math.min(cfg.limit, tokens);
}

// Read-only view of a key's 60-slot ring at simulated time nowRelNs.
function windowView(key, nowRelNs) {
  var st = sim.store.slidingWindow.windows.get(key);
  var windowNs = policy().windowNs;
  var g = st ? st.granularity : sim.store.slidingWindow.granularity;
  var bucketDur = st ? st.bucketDur : windowNs / BigInt(g);
  var nowNs = baseNs() + nowRelNs;
  var view = { slots: [], sum: 0, current: -1, oldestStartNs: null, bucketDur: bucketDur, g: g, active: 0 };
  var i;
  for (i = 0; i < g; i++) view.slots.push({ count: 0, live: false, current: false });
  if (!st) return view;
  var windowStart = nowNs - windowNs;
  var cur = Number((nowNs / bucketDur) % BigInt(g));
  var curStart = A.truncate(nowNs, bucketDur);
  for (i = 0; i < g; i++) {
    var b = st.buckets[i];
    if (b.startNs === null || b.startNs < windowStart) continue;
    var age = (cur - i + g) % g; // 0 = current slot, g-1 = oldest possible
    var pos = g - 1 - age; // left (oldest) to right (newest)
    view.slots[pos].count = b.count;
    view.slots[pos].live = true;
    view.slots[pos].current = i === cur && b.startNs === curStart;
    if (i === cur) view.current = pos;
    view.sum += b.count;
    view.active++;
    if (view.oldestStartNs === null || b.startNs < view.oldestStartNs) view.oldestStartNs = b.startNs;
  }
  return view;
}

function adaptiveView(key) {
  var st = sim.store.adaptive.states.get(key);
  var floor = cfg.limit;
  return {
    state: st || null,
    floor: floor,
    cap: floor * C.MAX_FACTOR,
    step: Math.max(1, floor * 0.25),
    hist: sim.aimd.get(key) || [],
  };
}
