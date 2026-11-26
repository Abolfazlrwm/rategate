/*!
 * RateGate playground - algorithm ports.
 *
 * Line-by-line JavaScript ports of the Check paths in
 *   internal/algorithms/tokenbucket.go
 *   internal/algorithms/slidingwindow.go (exact ring + approximate mode)
 *   internal/algorithms/adaptive.go
 *
 * Times are BigInt nanoseconds since the Unix epoch, so Go's integer
 * Duration arithmetic (truncating division, modulo, time.Time.Truncate) is
 * reproduced exactly rather than approximately in floating point.
 *
 * test/algorithms.test.cjs replays the Go unit tests against this file.
 */
(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.RateGateAlgorithms = api;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  var NS_PER_MS = 1000000n;
  var NS_PER_S = 1000000000n;
  // Go's time.Time zero value (0001-01-01) expressed in Unix nanoseconds.
  var ZERO_TIME_UNIX_NS = -62135596800n * NS_PER_S;

  var TOKENBUCKET = "tokenbucket";
  var SLIDINGWINDOW = "slidingwindow";
  var ADAPTIVE = "adaptive";

  // ---------------------------------------------------------------- helpers

  // int64(f) in Go truncates toward zero.
  function truncInt64(f) {
    return Math.trunc(f);
  }

  // time.Duration.Seconds(): sec + nsec/1e9, both as float64.
  function secondsOf(dNs) {
    return Number(dNs / NS_PER_S) + Number(dNs % NS_PER_S) / 1e9;
  }

  // time.Duration(seconds * float64(time.Second))
  function durationFromSeconds(seconds) {
    return BigInt(Math.trunc(seconds * 1e9));
  }

  function msToNs(ms) {
    return BigInt(Math.round(ms * 1e6));
  }

  function nsToMs(ns) {
    return Number(ns) / 1e6;
  }

  function invalidPolicy(algo, reason) {
    var err = new Error("rategate: invalid policy for " + algo + ": " + reason);
    err.code = "ErrInvalidPolicy";
    err.algo = algo;
    err.reason = reason;
    return err;
  }

  // Policy.Validate: every field must be > 0 for a well-formed policy.
  function validate(policy, algo) {
    if (policy.limit <= 0) return invalidPolicy(algo, "limit must be > 0");
    if (algo === TOKENBUCKET) {
      if (policy.refillPerSecond <= 0) {
        return invalidPolicy(algo, "refill_per_second must be > 0");
      }
    } else if (algo === SLIDINGWINDOW || algo === ADAPTIVE) {
      if (policy.windowNs <= 0n) return invalidPolicy(algo, "window must be > 0");
    }
    return null;
  }

  function denied(policy, nowNs, err) {
    return {
      decision: {
        allowed: false,
        remaining: 0,
        limit: policy.limit,
        resetAtNs: nowNs,
        retryAfterNs: 0n,
      },
      error: err,
      explain: null,
    };
  }

  // ---------------------------------------------------------- token bucket

  function TokenBucket() {
    this.buckets = new Map();
  }

  TokenBucket.prototype.name = function () {
    return TOKENBUCKET;
  };

  // Limit is the bucket capacity (burst size); RefillPerSecond is the
  // steady-state refill rate. Refill is lazy: computed on each Check from the
  // time since lastFill, capped at capacity. One token is consumed per admit.
  TokenBucket.prototype.check = function (key, policy, nowNs) {
    var err = validate(policy, TOKENBUCKET);
    if (err) return denied(policy, nowNs, err);

    var b = this.buckets.get(key);
    if (!b) {
      // New bucket starts full so a fresh client can burst up to `limit`.
      b = { tokens: policy.limit, lastFillNs: nowNs };
      this.buckets.set(key, b);
    }

    var tokensAtArrival = b.tokens;
    var elapsedNs = nowNs - b.lastFillNs;
    if (elapsedNs > 0n) {
      var refill = secondsOf(elapsedNs) * policy.refillPerSecond;
      b.tokens = Math.min(policy.limit, b.tokens + refill);
      b.lastFillNs = nowNs;
    }

    var allowed = b.tokens >= 1.0;
    var tokensAtDecision = b.tokens;
    if (allowed) b.tokens -= 1.0;

    var remaining = truncInt64(b.tokens);
    // ResetAt: when the bucket is full again from the current state.
    var tokensNeeded = policy.limit - b.tokens;
    var secondsToFull = tokensNeeded / policy.refillPerSecond;

    var d = {
      allowed: allowed,
      remaining: remaining,
      limit: policy.limit,
      resetAtNs: nowNs + durationFromSeconds(secondsToFull),
      retryAfterNs: 0n,
    };
    if (!allowed) {
      // Time until one whole token is available.
      d.retryAfterNs = durationFromSeconds((1.0 - b.tokens) / policy.refillPerSecond);
    }
    return {
      decision: d,
      error: null,
      explain: {
        tokensAtArrival: tokensAtArrival,
        tokensAtDecision: tokensAtDecision,
        tokensAfter: b.tokens,
      },
    };
  };

  TokenBucket.prototype.reset = function () {
    this.buckets = new Map();
  };

  // -------------------------------------------------------- sliding window

  function SlidingWindow(opts) {
    opts = opts || {};
    this.granularity = opts.granularity > 0 ? opts.granularity : 60;
    this.approximate = !!opts.approximate;
    this.windows = new Map();
  }

  SlidingWindow.prototype.name = function () {
    return SLIDINGWINDOW;
  };

  // time.Time.Truncate(d): round down to a multiple of d since the zero time.
  function truncate(ns, d) {
    return ns - ((ns - ZERO_TIME_UNIX_NS) % d);
  }

  function newSWState(granularity, windowNs, nowNs, approx) {
    var bucketDur = windowNs / BigInt(granularity);
    if (bucketDur <= 0n) bucketDur = NS_PER_MS;
    var buckets = new Array(granularity);
    for (var i = 0; i < granularity; i++) buckets[i] = { startNs: null, count: 0 };
    return {
      granularity: granularity,
      bucketDur: bucketDur,
      buckets: buckets,
      lastRotateNs: nowNs,
      approxCount: 0,
      lastDecayNs: approx ? nowNs : null,
    };
  }

  SlidingWindow.prototype.check = function (key, policy, nowNs) {
    var err = validate(policy, SLIDINGWINDOW);
    if (err) return denied(policy, nowNs, err);

    var st = this.windows.get(key);
    if (!st) {
      st = newSWState(this.granularity, policy.windowNs, nowNs, this.approximate);
      this.windows.set(key, st);
    }
    if (this.approximate) return this.checkApprox(st, policy, nowNs);
    return this.checkExact(st, policy, nowNs);
  };

  // A request is allowed if the count of events in the trailing window is
  // strictly less than Limit. Events live in a ring of `granularity` sub-
  // buckets; stale buckets are expired lazily on each Check.
  SlidingWindow.prototype.checkExact = function (st, policy, nowNs) {
    var windowNs = policy.windowNs;
    var windowStart = nowNs - windowNs;
    var bucketDur = st.bucketDur;
    var buckets = st.buckets;
    var i;
    var b;

    var currentBucketIdx = Number((nowNs / bucketDur) % BigInt(this.granularity));
    var currentBucketStart = truncate(nowNs, bucketDur);

    // Expire buckets older than the trailing window.
    for (i = 0; i < buckets.length; i++) {
      b = buckets[i];
      if (b.startNs === null) continue;
      if (b.startNs < windowStart) {
        b.startNs = null;
        b.count = 0;
      }
    }

    // Ensure the current bucket exists (new period resets the slot).
    var cb = buckets[currentBucketIdx];
    if (cb.startNs !== currentBucketStart) {
      cb.startNs = currentBucketStart;
      cb.count = 0;
    }

    var sum = 0;
    for (i = 0; i < buckets.length; i++) sum += buckets[i].count;

    var allowed = sum < policy.limit;
    var sumBefore = sum;
    if (allowed) {
      cb.count++;
      sum++;
    }

    var d = {
      allowed: allowed,
      remaining: policy.limit - sum,
      limit: policy.limit,
      resetAtNs: nowNs + windowNs,
      retryAfterNs: 0n,
    };
    var oldestStartNs = null;
    if (!allowed) {
      // RetryAfter: until the oldest contributing bucket expires.
      var oldest = nowNs;
      var found = false;
      for (i = 0; i < buckets.length; i++) {
        b = buckets[i];
        if (b.count > 0 && (!found || b.startNs < oldest)) {
          oldest = b.startNs;
          found = true;
        }
      }
      var expire = oldest + windowNs;
      if (expire > nowNs) d.retryAfterNs = expire - nowNs;
      oldestStartNs = found ? oldest : null;
    }
    return {
      decision: d,
      error: null,
      explain: {
        sumBefore: sumBefore,
        sumAfter: sum,
        currentBucketIdx: currentBucketIdx,
        currentBucketStartNs: currentBucketStart,
        bucketDur: bucketDur,
        windowStartNs: windowStart,
        oldestStartNs: oldestStartNs,
      },
    };
  };

  // Exponentially-decaying estimate of the trailing-window count: O(1)
  // memory for very high cardinality keys, ~1% error.
  SlidingWindow.prototype.checkApprox = function (st, policy, nowNs) {
    var tau = secondsOf(policy.windowNs) / 2.0;
    var dt = secondsOf(nowNs - st.lastDecayNs);
    if (dt > 0) {
      st.approxCount *= Math.exp(-dt / tau);
      st.lastDecayNs = nowNs;
    }

    var allowed = st.approxCount < policy.limit;
    if (allowed) st.approxCount += 1.0;

    var d = {
      allowed: allowed,
      remaining: truncInt64(policy.limit - st.approxCount),
      limit: policy.limit,
      resetAtNs: nowNs + policy.windowNs,
      retryAfterNs: 0n,
    };
    if (!allowed) {
      // Time for the estimate to decay back below the limit.
      if (st.approxCount > policy.limit) {
        var t = -tau * Math.log(policy.limit / st.approxCount);
        if (t > 0) d.retryAfterNs = durationFromSeconds(t);
      }
      if (d.retryAfterNs <= 0n) d.retryAfterNs = policy.windowNs / 2n;
    }
    return { decision: d, error: null, explain: { approxCount: st.approxCount } };
  };

  SlidingWindow.prototype.reset = function () {
    this.windows = new Map();
  };

  // ----------------------------------------------------- adaptive (AIMD)

  function AdaptiveLimiter(opts) {
    opts = opts || {};
    this.maxFactor = opts.maxFactor > 1 ? opts.maxFactor : 4;
    this.increaseFrac = opts.increaseFrac > 0 ? opts.increaseFrac : 0.25;
    this.decreaseFactor =
      opts.decreaseFactor > 0 && opts.decreaseFactor < 1 ? opts.decreaseFactor : 0.5;
    this.states = new Map();
  }

  AdaptiveLimiter.prototype.name = function () {
    return ADAPTIVE;
  };

  // Policy.Limit is the steady-state floor; Policy.Window is the AIMD
  // sampling window; RefillPerSecond is ignored. The effective limit lives in
  // [floor, floor*MaxFactor] and moves once per window boundary.
  AdaptiveLimiter.prototype.check = function (key, policy, nowNs) {
    var err = validate(policy, ADAPTIVE);
    if (err) return denied(policy, nowNs, err);

    var floor = policy.limit;
    var limitCap = floor * this.maxFactor;
    var increaseStep = Math.max(1.0, floor * this.increaseFrac);

    var st = this.states.get(key);
    if (!st) {
      st = { limit: floor, windowStartNs: nowNs, prevTotal: 0, count: 0, rejects: 0 };
      this.states.set(key, st);
    }

    // Close the window if it has elapsed.
    var windowEndNs = st.windowStartNs + policy.windowNs;
    var closed = null;
    if (nowNs >= windowEndNs) {
      var total = st.count + st.rejects;
      var limitBefore = st.limit;
      if (total > st.prevTotal && st.limit < limitCap) {
        // Demand rising: additive increase toward the cap.
        st.limit = Math.min(limitCap, st.limit + increaseStep);
      } else if (total < st.prevTotal && st.limit > floor) {
        // Demand falling: multiplicative decrease toward the floor.
        st.limit = Math.max(floor, st.limit * this.decreaseFactor);
      }
      closed = {
        total: total,
        admitted: st.count,
        rejected: st.rejects,
        prevTotal: st.prevTotal,
        limitBefore: limitBefore,
        limitAfter: st.limit,
        windowStartNs: st.windowStartNs,
        windowEndNs: windowEndNs,
      };
      st.prevTotal = total;
      st.windowStartNs = nowNs;
      st.count = 0;
      st.rejects = 0;
      windowEndNs = nowNs + policy.windowNs;
    }

    var effLimit = truncInt64(st.limit);
    var allowed = st.count < effLimit;
    var remaining;
    if (allowed) {
      st.count++;
      remaining = effLimit - st.count;
    } else {
      st.rejects++;
      remaining = 0;
    }

    return {
      decision: {
        allowed: allowed,
        remaining: remaining,
        limit: effLimit,
        resetAtNs: windowEndNs,
        retryAfterNs: allowed ? 0n : windowEndNs - nowNs,
      },
      error: null,
      explain: {
        admittedThisWindow: st.count,
        rejectedThisWindow: st.rejects,
        prevTotal: st.prevTotal,
        effLimit: effLimit,
        floor: floor,
        limitCap: limitCap,
        increaseStep: increaseStep,
        windowStartNs: st.windowStartNs,
        windowEndNs: windowEndNs,
        closed: closed,
      },
    };
  };

  AdaptiveLimiter.prototype.reset = function () {
    this.states = new Map();
  };

  // ---------------------------------------------------------- memory store

  // Mirrors store.MemoryStore: one limiter per algorithm, each keyed by the
  // bare key (state is NOT partitioned by policy, exactly like the server).
  function MemoryStore(opts) {
    opts = opts || {};
    this.tokenBucket = new TokenBucket();
    this.slidingWindow = new SlidingWindow({
      granularity: opts.swGranularity,
      approximate: opts.swApproximate,
    });
    this.adaptive = new AdaptiveLimiter({ maxFactor: opts.adaptiveMaxFactor });
  }

  MemoryStore.prototype.name = function () {
    return "memory";
  };

  MemoryStore.prototype.check = function (algo, key, policy, nowNs) {
    switch (algo) {
      case TOKENBUCKET:
        return this.tokenBucket.check(key, policy, nowNs);
      case SLIDINGWINDOW:
        return this.slidingWindow.check(key, policy, nowNs);
      case ADAPTIVE:
        return this.adaptive.check(key, policy, nowNs);
      default: {
        var err = new Error("rategate: unknown algorithm " + JSON.stringify(algo));
        err.code = "ErrUnknownAlgorithm";
        return denied(policy, nowNs, err);
      }
    }
  };

  MemoryStore.prototype.reset = function () {
    this.tokenBucket.reset();
    this.slidingWindow.reset();
    this.adaptive.reset();
  };

  return {
    TOKENBUCKET: TOKENBUCKET,
    SLIDINGWINDOW: SLIDINGWINDOW,
    ADAPTIVE: ADAPTIVE,
    validate: validate,
    TokenBucket: TokenBucket,
    SlidingWindow: SlidingWindow,
    AdaptiveLimiter: AdaptiveLimiter,
    MemoryStore: MemoryStore,
    // time.Time.Truncate(d) and the zero time it counts from, for read-only
    // views of a sliding-window state (see windowView in the playground).
    truncate: truncate,
    ZERO_TIME_UNIX_NS: ZERO_TIME_UNIX_NS,
    NS_PER_MS: NS_PER_MS,
    NS_PER_S: NS_PER_S,
    msToNs: msToNs,
    nsToMs: nsToMs,
    secondsOf: secondsOf,
    durationFromSeconds: durationFromSeconds,
  };
});
