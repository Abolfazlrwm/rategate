"use strict";
/*!
 * Replays the Go unit tests in internal/algorithms/*_test.go against the
 * JavaScript ports, then adds randomized invariant checks.
 *
 *   node --test test/
 */
const test = require("node:test");
const assert = require("node:assert/strict");
const A = require("../src/algorithms.js");

const NS_PER_MS = A.NS_PER_MS;
const NS_PER_S = A.NS_PER_S;
const at = (iso) => BigInt(Date.parse(iso)) * NS_PER_MS;
const sec = (n) => BigInt(n) * NS_PER_S;
const ms = (n) => BigInt(n) * NS_PER_MS;
const ok = (r) => {
  assert.equal(r.error, null);
  return r.decision;
};

// ------------------------------------------------------------- token bucket

test("TokenBucket.BasicAllowThenDeny", () => {
  const tb = new A.TokenBucket();
  const policy = { limit: 5, refillPerSecond: 1 };
  const now = at("2025-11-12T10:00:00Z");
  for (let i = 0; i < 5; i++) {
    const d = ok(tb.check("user:1", policy, now));
    assert.equal(d.allowed, true, `check ${i} allowed`);
    assert.equal(d.remaining, 5 - (i + 1));
    assert.equal(d.limit, 5);
    assert.equal(d.retryAfterNs, 0n);
  }
  const d = ok(tb.check("user:1", policy, now));
  assert.equal(d.allowed, false);
  assert.equal(d.remaining, 0);
  assert.ok(d.retryAfterNs > 0n);
  assert.equal(tb.name(), "tokenbucket");
});

test("TokenBucket.RefillOverTime", () => {
  const tb = new A.TokenBucket();
  const policy = { limit: 2, refillPerSecond: 2 };
  const t0 = at("2025-11-12T10:00:00Z");
  assert.equal(ok(tb.check("k", policy, t0)).allowed, true);
  assert.equal(ok(tb.check("k", policy, t0)).allowed, true);
  assert.equal(ok(tb.check("k", policy, t0)).allowed, false);

  const t1 = t0 + ms(500);
  assert.equal(ok(tb.check("k", policy, t1)).allowed, true, "1 token after 500ms");
  assert.equal(ok(tb.check("k", policy, t1)).allowed, false);

  const t2 = t1 + sec(1);
  assert.equal(ok(tb.check("k", policy, t2)).allowed, true);
  assert.equal(ok(tb.check("k", policy, t2)).allowed, true);
});

test("TokenBucket.RefillCappedAtCapacity", () => {
  const tb = new A.TokenBucket();
  const policy = { limit: 3, refillPerSecond: 100 };
  const t0 = at("2025-11-12T10:00:00Z");
  for (let i = 0; i < 3; i++) assert.equal(ok(tb.check("k", policy, t0)).allowed, true);
  const t1 = t0 + sec(3600);
  for (let i = 0; i < 3; i++) {
    assert.equal(ok(tb.check("k", policy, t1)).allowed, true, "capped refill");
  }
  assert.equal(ok(tb.check("k", policy, t1)).allowed, false);
});

test("TokenBucket.IndependentKeys", () => {
  const tb = new A.TokenBucket();
  const policy = { limit: 1, refillPerSecond: 0.001 };
  const now = at("2025-11-12T10:00:00Z");
  assert.equal(ok(tb.check("a", policy, now)).allowed, true);
  assert.equal(ok(tb.check("b", policy, now)).allowed, true);
  assert.equal(ok(tb.check("a", policy, now)).allowed, false);
  assert.equal(ok(tb.check("b", policy, now)).allowed, false);
});

test("TokenBucket.InvalidPolicy", () => {
  const tb = new A.TokenBucket();
  const now = at("2025-11-12T10:00:00Z");
  const cases = [
    { limit: 0, refillPerSecond: 1 },
    { limit: -1, refillPerSecond: 1 },
    { limit: 5, refillPerSecond: 0 },
    { limit: 5, refillPerSecond: -1 },
  ];
  cases.forEach((p, i) => {
    const r = tb.check("k", p, now);
    assert.ok(r.error, `case ${i} errors`);
    assert.equal(r.error.code, "ErrInvalidPolicy");
    assert.equal(r.decision.allowed, false);
    assert.equal(r.decision.limit, p.limit);
  });
});

test("TokenBucket.ResetAtIncreasing", () => {
  const tb = new A.TokenBucket();
  const policy = { limit: 2, refillPerSecond: 1 };
  const t0 = at("2025-11-12T10:00:00Z");
  ok(tb.check("k", policy, t0));
  const d2 = ok(tb.check("k", policy, t0));
  assert.equal(d2.resetAtNs, t0 + sec(2));
});

test("TokenBucket.remaining truncates fractional tokens and retry-after is exact", () => {
  const tb = new A.TokenBucket();
  const policy = { limit: 3, refillPerSecond: 2 };
  const t0 = at("2025-11-12T10:00:00Z");
  for (let i = 0; i < 3; i++) ok(tb.check("k", policy, t0));

  const half = ok(tb.check("k", policy, t0 + ms(250)));
  assert.equal(half.allowed, false, "0.5 tokens is not enough");
  assert.equal(half.retryAfterNs, ms(250));

  const one = ok(tb.check("k", policy, t0 + ms(500)));
  assert.equal(one.allowed, true);
  assert.equal(one.remaining, 0, "int64(0.0 tokens) is 0");
});

test("TokenBucket.Reset starts every bucket full again", () => {
  const tb = new A.TokenBucket();
  const policy = { limit: 2, refillPerSecond: 0.001 };
  const now = at("2025-11-12T10:00:00Z");
  ok(tb.check("a", policy, now));
  ok(tb.check("b", policy, now));
  ok(tb.check("c", policy, now));
  assert.equal(tb.buckets.size, 3);
  tb.reset();
  assert.equal(tb.buckets.size, 0);
  assert.equal(ok(tb.check("a", policy, now)).allowed, true);
});

// ------------------------------------------------------------ sliding window

test("SlidingWindow.BasicAllowThenDeny", () => {
  const sw = new A.SlidingWindow({ granularity: 10 });
  const policy = { limit: 3, windowNs: sec(60) };
  const now = at("2025-11-12T10:00:00Z");
  for (let i = 0; i < 3; i++) {
    const d = ok(sw.check("u", policy, now));
    assert.equal(d.allowed, true);
    assert.equal(d.remaining, 3 - (i + 1));
  }
  const d = ok(sw.check("u", policy, now));
  assert.equal(d.allowed, false);
  assert.equal(d.remaining, 0);
  assert.ok(d.retryAfterNs > 0n);
  assert.equal(sw.name(), "slidingwindow");
});

test("SlidingWindow.WindowRolls", () => {
  const sw = new A.SlidingWindow({ granularity: 6 });
  const policy = { limit: 2, windowNs: sec(60) };
  const t0 = at("2025-11-12T10:00:00Z");
  assert.equal(ok(sw.check("k", policy, t0)).allowed, true);
  assert.equal(ok(sw.check("k", policy, t0)).allowed, true);
  assert.equal(ok(sw.check("k", policy, t0 + sec(5))).allowed, false);
  assert.equal(ok(sw.check("k", policy, t0 + sec(70))).allowed, true);
});

test("SlidingWindow.PartialRoll", () => {
  const sw = new A.SlidingWindow({ granularity: 6 });
  const policy = { limit: 3, windowNs: sec(60) };
  const t0 = at("2025-11-12T10:00:00Z");
  ok(sw.check("k", policy, t0));
  ok(sw.check("k", policy, t0 + sec(15)));
  ok(sw.check("k", policy, t0 + sec(25)));
  assert.equal(ok(sw.check("k", policy, t0 + sec(25))).allowed, false);
  assert.equal(ok(sw.check("k", policy, t0 + sec(65))).allowed, true);
});

test("SlidingWindow.RetryAfterPointsAtOldestBucketExpiry", () => {
  const sw = new A.SlidingWindow({ granularity: 6 });
  const policy = { limit: 2, windowNs: sec(60) };
  const t0 = at("2025-11-12T10:00:00Z");
  ok(sw.check("k", policy, t0));
  ok(sw.check("k", policy, t0));
  const d = ok(sw.check("k", policy, t0 + sec(5)));
  assert.equal(d.allowed, false);
  assert.equal(d.retryAfterNs, sec(55));
  assert.equal(d.resetAtNs, t0 + sec(65));
});

test("SlidingWindow.IndependentKeys", () => {
  const sw = new A.SlidingWindow({ granularity: 6 });
  const policy = { limit: 1, windowNs: sec(60) };
  const now = at("2025-11-12T10:00:00Z");
  assert.equal(ok(sw.check("a", policy, now)).allowed, true);
  assert.equal(ok(sw.check("b", policy, now)).allowed, true);
  assert.equal(ok(sw.check("a", policy, now)).allowed, false);
});

test("SlidingWindow.InvalidPolicy", () => {
  const sw = new A.SlidingWindow({ granularity: 6 });
  const now = at("2025-11-12T10:00:00Z");
  [
    { limit: 0, windowNs: sec(60) },
    { limit: 5, windowNs: 0n },
    { limit: -1, windowNs: sec(60) },
  ].forEach((p, i) => {
    const r = sw.check("k", p, now);
    assert.ok(r.error, `case ${i} errors`);
    assert.equal(r.decision.allowed, false);
  });
});

test("SlidingWindow.writes each request into the sub-bucket its timestamp falls in", () => {
  const sw = new A.SlidingWindow({ granularity: 60 });
  const policy = { limit: 1000, windowNs: sec(60) };
  const t0 = at("2026-11-16T10:00:00Z");
  const st = () => sw.windows.get("k");

  ok(sw.check("k", policy, t0 + sec(20) + ms(250)));
  assert.equal(st().bucketDur, sec(1));
  const written = st().buckets.filter((b) => b.count === 1);
  assert.equal(written.length, 1);
  assert.equal(written[0].startNs, t0 + sec(20));

  // One full ring later the same slot is reused for a new period.
  const slotOf = () => st().buckets.findIndex((b) => b.count > 0 && b.startNs !== null);
  const first = slotOf();
  ok(sw.check("k", policy, t0 + sec(80) + ms(250)));
  const slots = st().buckets.map((b, i) => (b.count > 0 ? i : -1)).filter((i) => i >= 0);
  assert.deepEqual(slots, [first], "same ring slot 60s later");
  assert.equal(st().buckets[first].startNs, t0 + sec(80));
  assert.equal(st().buckets[first].count, 1, "the old period was reset, not summed");
});

test("SlidingWindow.sub-bucket boundary moves the write slot", () => {
  const sw = new A.SlidingWindow({ granularity: 6 });
  const policy = { limit: 100, windowNs: sec(60) };
  const t0 = at("2026-11-16T10:00:00Z");
  ok(sw.check("k", policy, t0 + sec(10) - 1n));
  ok(sw.check("k", policy, t0 + sec(10) + 1n));
  const used = sw.windows.get("k").buckets.filter((b) => b.count > 0);
  assert.equal(used.length, 2);
  assert.notEqual(used[0].startNs, used[1].startNs);
});

test("SlidingWindow.TinyWindowFallback (bucketDur clamps to 1ms)", () => {
  const sw = new A.SlidingWindow({ granularity: 2 });
  ok(sw.check("k", { limit: 5, windowNs: 1n }, at("2026-11-16T10:00:00Z")));
  assert.equal(sw.windows.get("k").bucketDur, ms(1));
});

test("SlidingWindow.Approximate denies at the limit with a positive retry", () => {
  const sw = new A.SlidingWindow({ granularity: 6, approximate: true });
  const policy = { limit: 1, windowNs: sec(1) };
  const t0 = at("2025-11-12T10:00:00Z");
  assert.equal(ok(sw.check("k", policy, t0)).allowed, true);
  const d = ok(sw.check("k", policy, t0));
  assert.equal(d.allowed, false);
  assert.ok(d.retryAfterNs > 0n);
  assert.equal(d.limit, 1);
});

// ------------------------------------------------------- adaptive (AIMD)

function sendAt(lim, key, policy, nowNs, n) {
  let admitted = 0;
  let denied = 0;
  let firstLimit = null;
  for (let i = 0; i < n; i++) {
    const d = ok(lim.check(key, policy, nowNs));
    if (i === 0) firstLimit = d.limit;
    if (d.allowed) admitted++;
    else denied++;
  }
  return { admitted, denied, firstLimit };
}

test("AdaptiveLimiter.StartsAtFloor", () => {
  const lim = new A.AdaptiveLimiter({ maxFactor: 4 });
  const policy = { limit: 5, windowNs: sec(1) };
  const t0 = at("2026-11-16T10:00:00Z");
  const w = sendAt(lim, "k", policy, t0, 8);
  assert.equal(w.firstLimit, 5);
  assert.deepEqual([w.admitted, w.denied], [5, 3]);
  const d = ok(lim.check("k", policy, t0));
  assert.equal(d.allowed, false);
  assert.ok(d.retryAfterNs > 0n && d.retryAfterNs <= sec(1));
  assert.equal(lim.name(), "adaptive");
});

test("AdaptiveLimiter.GrowsOnRisingDemand (exact trajectory, saturates at cap)", () => {
  const lim = new A.AdaptiveLimiter({ maxFactor: 4, increaseFrac: 0.25 });
  const policy = { limit: 10, windowNs: sec(1) };
  const t0 = at("2026-11-16T10:00:00Z");
  let prev = 10;
  for (let w = 0; w < 20; w++) {
    const { firstLimit } = sendAt(lim, "k", policy, t0 + BigInt(w) * sec(1), 10 * (w + 1));
    assert.ok(firstLimit >= prev, `window ${w}: ${firstLimit} < ${prev}`);
    assert.ok(firstLimit <= 40, `window ${w}: ${firstLimit} > cap`);
    // step = max(1, 10 * 0.25) = 2.5, int64-truncated; cap 40 from window 12 on.
    assert.equal(firstLimit, Math.min(40, Math.trunc(10 + 2.5 * w)));
    prev = firstLimit;
  }
  assert.equal(prev, 40);
});

test("AdaptiveLimiter.ShrinksOnFallingDemand", () => {
  const lim = new A.AdaptiveLimiter({ maxFactor: 4, increaseFrac: 0.25, decreaseFactor: 0.5 });
  const policy = { limit: 10, windowNs: sec(1) };
  const t0 = at("2026-11-16T10:00:00Z");
  const trajectory = [];
  for (let w = 0; w < 9; w++) {
    trajectory.push(sendAt(lim, "k", policy, t0 + BigInt(w) * sec(1), 10 * (w + 1)).firstLimit);
  }
  assert.deepEqual(trajectory, [10, 12, 15, 17, 20, 22, 25, 27, 30]);
  const peak = sendAt(lim, "k", policy, t0 + sec(9), 1).firstLimit;
  assert.equal(peak, 32);
  const shrunk = sendAt(lim, "k", policy, t0 + sec(10), 1).firstLimit;
  assert.equal(shrunk, 16, "int64(32.5 * 0.5)");
  // The Go test only loops here without asserting. Constant demand is neither
  // rising nor falling, so hysteresis holds the limit: it never reaches the
  // floor on its own.
  let held = shrunk;
  for (let w = 11; w < 20; w++) {
    held = sendAt(lim, "k", policy, t0 + BigInt(w) * sec(1), 1).firstLimit;
  }
  assert.equal(held, 16, "steady demand holds the limit (no drift to the floor)");
});

test("AdaptiveLimiter.strictly falling demand walks the limit back to the floor", () => {
  const lim = new A.AdaptiveLimiter({ maxFactor: 4, increaseFrac: 0.25, decreaseFactor: 0.5 });
  const policy = { limit: 10, windowNs: sec(1) };
  const t0 = at("2026-11-16T10:00:00Z");
  // Grow to 30 with rising demand (10, 20, ..., 90).
  for (let w = 0; w < 9; w++) sendAt(lim, "k", policy, t0 + BigInt(w) * sec(1), 10 * (w + 1));
  // Then 60, 50, 40, 30: every window's total is below the previous one, so
  // the limit halves per boundary until it lands on the floor: 32 -> 16 -> 10.
  const limits = [60, 50, 40, 30].map((n, i) =>
    sendAt(lim, "k", policy, t0 + BigInt(9 + i) * sec(1), n).firstLimit);
  assert.deepEqual(limits, [32, 16, 10, 10]);
});

test("AdaptiveLimiter.SteadyDemandHoldsLimit (hysteresis)", () => {
  const lim = new A.AdaptiveLimiter({ maxFactor: 4 });
  const policy = { limit: 10, windowNs: sec(1) };
  const t0 = at("2026-11-16T10:00:00Z");
  sendAt(lim, "k", policy, t0, 20);
  const lim2 = sendAt(lim, "k", policy, t0 + sec(1), 20).firstLimit;
  const lim3 = sendAt(lim, "k", policy, t0 + sec(2), 20).firstLimit;
  assert.ok(lim2 > 10);
  assert.equal(lim3, lim2);
});

test("AdaptiveLimiter.KeysAreIndependent", () => {
  const lim = new A.AdaptiveLimiter({ maxFactor: 4 });
  const policy = { limit: 10, windowNs: sec(1) };
  const t0 = at("2026-11-16T10:00:00Z");
  for (let w = 0; w < 5; w++) sendAt(lim, "A", policy, t0 + BigInt(w) * sec(1), 10 * (w + 1));
  const limitA = sendAt(lim, "A", policy, t0 + sec(5), 1).firstLimit;
  const limitB = sendAt(lim, "B", policy, t0 + sec(5), 1).firstLimit;
  assert.equal(limitB, 10);
  assert.ok(limitA > 10);
});

test("AdaptiveLimiter.ResetAtIsWindowBoundary", () => {
  const lim = new A.AdaptiveLimiter({});
  const d = ok(lim.check("k", { limit: 5, windowNs: sec(2) }, at("2026-11-16T10:00:00Z")));
  assert.equal(d.resetAtNs, at("2026-11-16T10:00:00Z") + sec(2));
});

test("AdaptiveLimiter.InvalidPolicy and defaults", () => {
  const lim = new A.AdaptiveLimiter({});
  assert.deepEqual([lim.maxFactor, lim.increaseFrac, lim.decreaseFactor], [4, 0.25, 0.5]);
  const now = at("2026-11-16T10:00:00Z");
  for (const p of [{ limit: 0, windowNs: sec(1) }, { limit: 5, windowNs: 0n }]) {
    assert.ok(lim.check("k", p, now).error);
  }
});

// ------------------------------------------------------------- memory store

test("MemoryStore dispatches by algorithm name and rejects unknown ones", () => {
  const store = new A.MemoryStore({ swGranularity: 60 });
  assert.equal(store.name(), "memory");
  const now = at("2026-11-16T10:00:00Z");
  assert.equal(ok(store.check("tokenbucket", "k", { limit: 1, refillPerSecond: 1 }, now)).allowed, true);
  assert.equal(ok(store.check("slidingwindow", "k", { limit: 1, windowNs: sec(1) }, now)).allowed, true);
  assert.equal(ok(store.check("adaptive", "k", { limit: 1, windowNs: sec(1) }, now)).allowed, true);
  const bad = store.check("nope", "k", { limit: 1 }, now);
  assert.equal(bad.error.code, "ErrUnknownAlgorithm");
  assert.equal(bad.decision.allowed, false);
  store.reset();
  assert.equal(ok(store.check("tokenbucket", "k", { limit: 1, refillPerSecond: 1 }, now)).allowed, true);
});

// ------------------------------------------------------- randomized invariants

function mulberry32(seed) {
  let a = seed;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Poisson arrivals at `rate` per second over `seconds`, as an array of ns.
function arrivals(rand, rate, seconds) {
  const out = [];
  let t = 0;
  for (;;) {
    t += -Math.log(1 - rand()) / rate;
    if (t >= seconds) break;
    out.push(BigInt(Math.round(t * 1e9)));
  }
  return out;
}

test("invariant: sliding window never admits more than `limit` in a trailing window", () => {
  const rand = mulberry32(42);
  const limit = 20;
  const windowNs = sec(30);
  const sw = new A.SlidingWindow({ granularity: 60 });
  const admitted = [];
  for (const t of arrivals(rand, 2, 900)) {
    if (ok(sw.check("k", { limit, windowNs }, t)).allowed) admitted.push(t);
  }
  // A sub-bucket is dropped once its START is older than now - window, so an
  // event sitting near the left edge can be forgotten up to one sub-bucket
  // (window / granularity) early. Inside the window minus that margin the
  // bound is exact.
  const margin = windowNs / 60n;
  for (let i = 0, j = 0; i < admitted.length; i++) {
    while (j + 1 < admitted.length && admitted[j + 1] - admitted[i] <= windowNs - margin) j++;
    assert.ok(j - i + 1 <= limit, `${j - i + 1} admitted within 30s`);
  }
});

test("SlidingWindow.forgets a sub-bucket once its start is a full window old (up to one sub-bucket early)", () => {
  const sw = new A.SlidingWindow({ granularity: 6 });
  const policy = { limit: 2, windowNs: sec(60) };
  const t0 = at("2026-11-16T10:00:00Z");
  // Both events land in the sub-bucket [t0+10s, t0+20s).
  ok(sw.check("k", policy, t0 + sec(15)));
  ok(sw.check("k", policy, t0 + sec(15)));
  // 1ns before that bucket's start is a window old it still counts.
  assert.equal(ok(sw.check("k", policy, t0 + sec(70) - 1n)).allowed, false);
  // At t0+70 it is dropped, although the events are only 55s old: the ring
  // slot is reused (or, equivalently, start < now - window) 5s early.
  assert.equal(ok(sw.check("k", policy, t0 + sec(70))).allowed, true);
});

test("invariant: token bucket never admits more than burst + refill x elapsed", () => {
  const rand = mulberry32(7);
  const limit = 10;
  const refill = 2;
  const tb = new A.TokenBucket();
  const admitted = [];
  for (const t of arrivals(rand, 5, 900)) {
    if (ok(tb.check("k", { limit, refillPerSecond: refill }, t)).allowed) admitted.push(t);
  }
  for (let i = 0; i < admitted.length; i++) {
    let j = i;
    while (j + 1 < admitted.length && admitted[j + 1] - admitted[i] <= sec(60)) j++;
    const span = Number(admitted[j] - admitted[i]) / 1e9;
    assert.ok(j - i + 1 <= limit + refill * span + 1e-6, `${j - i + 1} admitted in ${span}s`);
  }
});

test("invariant: adaptive effective limit stays in [floor, floor x maxFactor] and admits <= limit per window", () => {
  const rand = mulberry32(99);
  const floor = 8;
  const lim = new A.AdaptiveLimiter({ maxFactor: 4 });
  const policy = { limit: floor, windowNs: sec(2) };
  let admittedSinceClose = 0;
  let effLimit = floor;
  for (const t of arrivals(rand, 9, 600)) {
    const r = lim.check("k", policy, t);
    const d = ok(r);
    assert.ok(d.limit >= floor && d.limit <= floor * 4, `eff limit ${d.limit}`);
    if (r.explain.closed) {
      assert.ok(admittedSinceClose <= effLimit, `${admittedSinceClose} > ${effLimit}`);
      admittedSinceClose = 0;
    }
    effLimit = d.limit;
    if (d.allowed) admittedSinceClose++;
  }
});
