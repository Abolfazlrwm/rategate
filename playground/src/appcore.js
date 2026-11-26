/*!
 * RateGate playground - pure logic (no DOM).
 *
 * Everything here is deterministic and unit-tested in test/appcore.test.cjs:
 * config normalisation, policy construction, traffic shapes, key derivation,
 * formatters, URL (de)serialisation, the headless scenario runner used for
 * the scenario sparklines, and the code generators behind the export tabs.
 */
(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.RateGateCore = api;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  var NS_PER_MS = 1000000n;

  var ALGOS = ["tokenbucket", "slidingwindow", "adaptive"];
  // Window slider stops (milliseconds). The index is what the config carries,
  // so a shared link always lands on a round value.
  var WINDOWS_MS = [100, 200, 250, 500, 1000, 2000, 2500, 5000, 10000, 15000, 20000, 30000, 60000];
  // store.memory_adaptive_max_factor in config.example.yaml
  var MAX_FACTOR = 4;

  var DEFAULTS = {
    algo: "tokenbucket",
    key: "user:1",
    nKeys: 1,
    shape: "steady",
    rate: 12,
    speed: 1,
    limit: 10,
    refill: 8,
    windowIdx: 8,
  };

  // Per-algorithm defaults the sliders fall back to when switching.
  var ALGO_DEFAULTS = {
    tokenbucket: { limit: 10, refill: 8 },
    slidingwindow: { limit: 20, windowIdx: 8 },
    adaptive: { limit: 20, windowIdx: 5 },
  };

  var RATE_MIN = 1;
  var RATE_MAX = 200;
  var LIMIT_MAX = 200;
  var REFILL_MIN = 0.5;
  var REFILL_MAX = 50;
  var KEY_MAX = 48;

  // Intensity multipliers over simulated seconds t. Each averages ~1.0, so
  // "rate" is always the mean arrival rate.
  var SHAPES = {
    steady: function (t) {
      return 1;
    },
    // 0.6 s at 3.2x, 1.6 s near zero, 1.8 s at 1x, every 4 s.
    burst: function (t) {
      var m = t % 4;
      if (m < 0.6) return 3.2;
      if (m < 2.2) return 0.15;
      return 1;
    },
    // Sawtooth 0.1x -> 1.9x every 12 s.
    ramp: function (t) {
      return 0.1 + 1.8 * ((t % 12) / 12);
    },
    // Sine 0.2x -> 1.8x with an 8 s period.
    wave: function (t) {
      return 1 + 0.8 * Math.sin((2 * Math.PI * t) / 8);
    },
  };

  var SCENES = {
    burst: { algo: "tokenbucket", limit: 20, refill: 5, shape: "burst", rate: 40, speed: 1, keys: 1, key: "user:1", seconds: 12 },
    quota: { algo: "slidingwindow", limit: 3, windowIdx: 12, shape: "steady", rate: 6, speed: 16, keys: 1, key: "user:1", seconds: 150 },
    spike: { algo: "adaptive", limit: 20, windowIdx: 5, shape: "ramp", rate: 200, speed: 4, keys: 1, key: "api:search", seconds: 16 },
    tenants: { algo: "tokenbucket", limit: 10, refill: 5, shape: "steady", rate: 25, speed: 1, keys: 5, key: "tenant:acme", skew: 0.6, seconds: 12 },
    slow: { algo: "slidingwindow", limit: 8, windowIdx: 3, shape: "wave", rate: 30, speed: 1, keys: 1, key: "ip:10.0.0.1", seconds: 12 },
    trickle: { algo: "tokenbucket", limit: 5, refill: 10, shape: "steady", rate: 40, speed: 4, keys: 1, key: "worker:cron", seconds: 12 },
  };

  // ----------------------------------------------------------- config

  function defaults() {
    var d = {};
    for (var k in DEFAULTS) d[k] = DEFAULTS[k];
    return d;
  }

  function clamp(v, lo, hi) {
    return Math.min(hi, Math.max(lo, v));
  }

  function sanitizeKey(s) {
    // Printable characters only; surrounding whitespace is not part of a key.
    return String(s == null ? "" : s).replace(/[\u0000-\u001f\u007f]/g, "").trim().slice(0, KEY_MAX);
  }

  function normalize(raw) {
    var c = defaults();
    if (!raw || typeof raw !== "object") return c;
    if (ALGOS.indexOf(raw.algo) >= 0) c.algo = raw.algo;
    c.key = sanitizeKey(raw.key) || DEFAULTS.key;
    c.nKeys = [1, 3, 5].indexOf(Number(raw.nKeys)) >= 0 ? Number(raw.nKeys) : 1;
    if (SHAPES[raw.shape]) c.shape = raw.shape;
    var rate = Number(raw.rate);
    c.rate = rate >= RATE_MIN && rate <= RATE_MAX ? rate : DEFAULTS.rate;
    c.speed = [1, 4, 16].indexOf(Number(raw.speed)) >= 0 ? Number(raw.speed) : 1;
    var limit = Math.round(Number(raw.limit));
    c.limit = limit >= 1 && limit <= LIMIT_MAX ? limit : ALGO_DEFAULTS[c.algo].limit;
    // refill and window are remembered even for the algorithm that does not
    // use them, so switching back and forth keeps the sliders where they were.
    var refill = Number(raw.refill);
    c.refill = refill >= REFILL_MIN && refill <= REFILL_MAX ? refill : DEFAULTS.refill;
    var w = Math.round(Number(raw.windowIdx));
    c.windowIdx = w >= 0 && w < WINDOWS_MS.length ? w : DEFAULTS.windowIdx;
    return c;
  }

  function windowMsOf(cfg) {
    return WINDOWS_MS[cfg.windowIdx];
  }

  // The Policy the store sees, in the shape the JS ports expect.
  function policyOf(cfg) {
    if (cfg.algo === "tokenbucket") {
      return { limit: cfg.limit, refillPerSecond: cfg.refill, windowNs: 0n };
    }
    return { limit: cfg.limit, refillPerSecond: 0, windowNs: BigInt(windowMsOf(cfg)) * NS_PER_MS };
  }

  // Requests per second the policy lets through in the long run.
  function sustainOf(cfg, effLimit) {
    if (cfg.algo === "tokenbucket") return cfg.refill;
    var limit = effLimit || cfg.limit;
    return (limit * 1000) / windowMsOf(cfg);
  }

  function intensity(shape, t) {
    var f = SHAPES[shape] || SHAPES.steady;
    return f(t);
  }

  // "user:1" x3 -> user:1, user:2, user:3 (zero padding kept).
  // "tenant:acme" x2 -> tenant:acme:1, tenant:acme:2.
  function deriveKeys(base, n) {
    base = sanitizeKey(base);
    n = clamp(Math.round(n) || 1, 1, 5);
    var out = [];
    var m = /^(.*?)(\d+)$/.exec(base);
    for (var i = 0; i < n; i++) {
      if (n === 1) {
        out.push(base);
      } else if (m) {
        var num = String(parseInt(m[2], 10) + i);
        while (num.length < m[2].length) num = "0" + num;
        out.push(m[1] + num);
      } else {
        out.push(base + ":" + (i + 1));
      }
    }
    return out;
  }

  // ------------------------------------------------------------ sliders

  // Log scale between RATE_MIN and RATE_MAX. One slider step is +5.3%, so the
  // snap step is kept under half of that: every position maps to a rate that
  // maps back to the same position (asserted in the tests).
  function rateFromSlider(v) {
    var r = Math.exp((Math.log(RATE_MAX) * clamp(Number(v), 0, 100)) / 100);
    if (r < 3) return Math.round(r * 50) / 50;
    if (r < 10) return Math.round(r * 10) / 10;
    if (r < 20) return Math.round(r * 5) / 5;
    if (r < 25) return Math.round(r * 2) / 2;
    return Math.round(r);
  }

  function sliderFromRate(r) {
    return Math.round((100 * Math.log(clamp(Number(r) || 1, RATE_MIN, RATE_MAX))) / Math.log(RATE_MAX));
  }

  // ------------------------------------------------------------- random

  function mulberry32(seed) {
    var a = seed >>> 0;
    return function () {
      a = (a + 0x6d2b79f5) >>> 0;
      var t = a;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  // Poisson draws: Knuth for small means, a normal approximation above 30.
  function poisson(rand, lambda) {
    if (!(lambda > 0)) return 0;
    if (lambda > 30) {
      var u = Math.max(1e-9, rand());
      var v = rand();
      var z = Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v);
      return Math.max(0, Math.round(lambda + z * Math.sqrt(lambda)));
    }
    var L = Math.exp(-lambda);
    var k = 0;
    var p = 1;
    do {
      k++;
      p *= rand();
    } while (p > L);
    return k - 1;
  }

  // --------------------------------------------------------- formatters

  function fmtInt(n) {
    return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  }

  function fmtNum(x) {
    var ax = Math.abs(x);
    if (ax >= 100) return String(Math.round(x));
    if (ax >= 10) return String(Math.round(x * 10) / 10);
    return String(Math.round(x * 100) / 100);
  }

  function fmtWindow(ms) {
    if (ms < 1000) return ms + " ms";
    return ms % 1000 === 0 ? ms / 1000 + " s" : Math.round(ms / 100) / 10 + " s";
  }

  function fmtDur(ns) {
    if (!(ns > 0n)) return "0 ms";
    var ms = Number(ns / 1000000n);
    if (ms < 1000) return ms + " ms";
    var s = ms / 1000;
    if (s < 10) return Math.round(s * 10) / 10 + " s";
    if (s < 60) return Math.round(s) + " s";
    var m = Math.floor(s / 60);
    var r = Math.round(s - m * 60);
    if (m < 60) return r ? m + " m " + r + " s" : m + " m";
    return Math.floor(m / 60) + " h " + (m % 60) + " m";
  }

  function pad(n, w) {
    var s = String(n);
    while (s.length < w) s = "0" + s;
    return s;
  }

  function fmtClock(ns) {
    var total = Number(ns / 1000000n);
    var ms = total % 1000;
    var s = Math.floor(total / 1000);
    return pad(Math.floor(s / 3600), 2) + ":" + pad(Math.floor(s / 60) % 60, 2) + ":" + pad(s % 60, 2) + "." + pad(ms, 3);
  }

  // Short stamp for the decision feed: mm:ss.s
  function fmtStamp(ns) {
    var total = Number(ns / 1000000n);
    var s = Math.floor(total / 1000);
    return pad(Math.floor(s / 60), 2) + ":" + pad(s % 60, 2) + "." + Math.floor((total % 1000) / 100);
  }

  function yamlDuration(ms) {
    if (ms < 1000) return ms + "ms";
    return ms % 1000 === 0 ? ms / 1000 + "s" : Math.round(ms / 100) / 10 + "s";
  }

  function goDuration(ms) {
    if (ms < 1000) return ms + " * time.Millisecond";
    return (ms % 1000 === 0 ? ms / 1000 : Math.round(ms / 100) / 10) + " * time.Second";
  }

  function algoLabel(algo) {
    return algo === "slidingwindow" ? "sliding window" : algo === "adaptive" ? "adaptive AIMD" : "token bucket";
  }

  function shapeLabel(shape) {
    return { steady: "steady", burst: "bursts", ramp: "ramp", wave: "wave" }[shape] || "steady";
  }

  // --------------------------------------------------------- scenarios

  // Replays a scenario on a fresh MemoryStore and returns per-bin decisions.
  // Used for the sparklines, so what the card shows is what the playground
  // will do.
  function runScenario(A, scene, opts) {
    opts = opts || {};
    var bins = opts.bins || 60;
    var seed = opts.seed || 20261123;
    var seconds = scene.seconds || 12;
    var steps = Math.max(bins * 4, Math.ceil(seconds * 20));
    var dt = seconds / steps;
    var keys = deriveKeys(scene.key || "user:1", scene.keys || 1);
    var policy =
      scene.algo === "tokenbucket"
        ? { limit: scene.limit, refillPerSecond: scene.refill, windowNs: 0n }
        : { limit: scene.limit, refillPerSecond: 0, windowNs: BigInt(WINDOWS_MS[scene.windowIdx]) * NS_PER_MS };
    var store = new A.MemoryStore({ swGranularity: 60, adaptiveMaxFactor: MAX_FACTOR });
    var rand = mulberry32(seed);
    var base = 1700000000n * 1000000000n;
    var out = [];
    var ok = 0;
    var no = 0;
    var perKey = {};
    var i;
    var k;
    for (i = 0; i < bins; i++) out.push({ ok: 0, no: 0 });
    for (i = 0; i < steps; i++) {
      var t = i * dt;
      var n = poisson(rand, scene.rate * intensity(scene.shape, t) * dt);
      for (k = 0; k < n; k++) {
        var at = base + BigInt(Math.round((t + rand() * dt) * 1e9));
        var idx = 0;
        if (keys.length > 1) {
          idx = scene.skew && rand() < scene.skew ? 0 : 1 + Math.floor(rand() * (keys.length - 1));
        }
        var key = keys[Math.min(idx, keys.length - 1)];
        var d = store.check(scene.algo, key, policy, at).decision;
        var bin = out[Math.min(bins - 1, Math.floor((t / seconds) * bins))];
        if (d.allowed) {
          bin.ok++;
          ok++;
        } else {
          bin.no++;
          no++;
        }
        perKey[key] = perKey[key] || { ok: 0, no: 0 };
        perKey[key][d.allowed ? "ok" : "no"]++;
      }
    }
    return { bins: out, ok: ok, no: no, perKey: perKey, keys: keys, seconds: seconds };
  }

  // --------------------------------------------------------------- URL

  function toParams(cfg) {
    var p = {
      a: cfg.algo,
      l: String(cfg.limit),
      k: cfg.key,
      n: String(cfg.nKeys),
      s: cfg.shape,
      q: String(cfg.rate),
      x: String(cfg.speed),
    };
    // Both are sent so the URL round-trips exactly for any algorithm.
    p.r = String(cfg.refill);
    p.w = String(cfg.windowIdx);
    return p;
  }

  function fromParams(search) {
    var get = function (k) {
      var m = new RegExp("[?&]" + k + "=([^&]*)").exec(search);
      return m ? decodeURIComponent(m[1].replace(/\+/g, " ")) : null;
    };
    var a = get("a");
    if (!a || ALGOS.indexOf(a) < 0) return null;
    var raw = {
      algo: a,
      key: get("k") == null ? DEFAULTS.key : get("k"),
      nKeys: get("n"),
      shape: get("s"),
      rate: get("q"),
      speed: get("x"),
      limit: get("l"),
      refill: get("r"),
      windowIdx: get("w"),
    };
    return normalize(raw);
  }

  // ------------------------------------------------------ code export

  function escBash(s) {
    return "'" + String(s).replace(/'/g, "'\\''") + "'";
  }

  function goQuote(s) {
    return JSON.stringify(String(s));
  }

  function restBody(cfg) {
    var o = { key: cfg.key, algorithm: cfg.algo, limit: cfg.limit };
    if (cfg.algo === "tokenbucket") o.refill_per_second = cfg.refill;
    else o.window_ms = windowMsOf(cfg);
    return JSON.stringify(o);
  }

  function grpcBody(cfg) {
    var o = { key: cfg.key };
    if (cfg.algo === "tokenbucket") {
      o.algorithm = cfg.algo;
      o.limit = String(cfg.limit);
      o.refillPerSecond = cfg.refill;
    } else {
      o.algorithm = cfg.algo;
      o.limit = String(cfg.limit);
      o.windowMs = String(windowMsOf(cfg));
    }
    return JSON.stringify(o);
  }

  var SUMMARY = {
    tokenbucket: "burst {l}, refill {r} tok/s",
    slidingwindow: "{l} per {w}",
    adaptive: "floor {l}, window {w}",
  };

  function summary(cfg) {
    return SUMMARY[cfg.algo]
      .replace("{l}", String(cfg.limit))
      .replace("{r}", String(cfg.refill))
      .replace("{w}", fmtWindow(windowMsOf(cfg)));
  }

  // Each line is a list of [class, text] tokens; class "" means plain text.
  // Classes map onto the .code palette in style.css.
  function exportCode(cfg, tab) {
    var L = [];
    var line = function () {
      L.push(Array.prototype.slice.call(arguments));
    };
    var c = function (text) {
      return ["c", text];
    };
    var s = function (text) {
      return ["s", text];
    };
    var t = function (text) {
      return ["t", text];
    };
    var k = function (text) {
      return ["k", text];
    };
    var n = function (text) {
      return ["n", text];
    };
    var f = function (text) {
      return ["f", text];
    };
    var plain = function (text) {
      return ["", text];
    };
    var head = "# " + algoLabel(cfg.algo) + " · " + summary(cfg) + " — the playground's policy";
    var w = windowMsOf(cfg);

    if (tab === "curl") {
      line(c(head));
      line(plain("curl -i -X POST localhost:8080/v1/check \\"));
      line(plain("  -H "), s("'Content-Type: application/json'"), plain(" \\"));
      line(plain("  -d "), s(escBash(restBody(cfg))));
      line();
      line(c("# 200 OK when allowed; 429 + Retry-After (whole seconds) when denied."));
      return L;
    }

    if (tab === "grpcurl") {
      line(c(head));
      line(c("# Reflection is enabled on the server, so no proto files are needed."));
      line(plain("grpcurl -plaintext \\"));
      line(plain("  -d "), s(escBash(grpcBody(cfg))), plain(" \\"));
      line(plain("  localhost:9090 rategate.v1.RateGateService/CheckRateLimit"));
      line();
      line(c("# int64 fields travel as JSON strings in both directions."));
      return L;
    }

    if (tab === "go") {
      var fields = [
        ["Key", "user-key"],
        ["Algorithm", "algo"],
        ["Limit", String(cfg.limit)],
      ];
      if (cfg.algo === "tokenbucket") fields.push(["RefillPerSecond", String(cfg.refill)]);
      else fields.push(["WindowMs", String(w)]);
      var width = 0;
      fields.forEach(function (fd) {
        width = Math.max(width, fd[0].length);
      });
      line(k("import"), plain(" "), s("\"errors\""));
      line(k("import"), plain(" "), s("\"github.com/Abolfazlrwm/rategate/pkg/client\""));
      line();
      line(plain("limiter "), k(":="), plain(" "), t("client"), plain("."), f("NewHTTPClient"), plain("("), s("\"http://localhost:8080\""), plain(")"));
      line();
      line(plain("resp, err "), k(":="), plain(" limiter."), f("Check"), plain("(ctx, "), t("client"), plain("."), t("CheckRequest"), plain("{"));
      fields.forEach(function (fd) {
        var pad = new Array(width - fd[0].length + 2).join(" ");
        var value =
          fd[1] === "user-key"
            ? s(goQuote(cfg.key))
            : fd[1] === "algo"
              ? s(goQuote(cfg.algo))
              : n(fd[1]);
        line(plain("    " + fd[0] + ":" + pad), value, plain(","));
      });
      line(plain("})"));
      line();
      line(k("var"), plain(" denied *"), t("client"), plain("."), t("HTTPError"));
      line(k("if"), plain(" "), t("errors"), plain("."), f("As"), plain("(err, &denied) && denied."), f("IsDenied"), plain("() {"));
      line(c("    // 429. resp is nil here; denied.Body holds the JSON decision,"));
      line(c("    // including retry_after_ms and reset_at_unix_ms."));
      line(plain("    "), k("return"));
      line(plain("}"));
      line(k("if"), plain(" err != "), k("nil"), plain(" {"));
      line(c("    // Transport failure or a 5xx: decide whether to fail open."));
      line(plain("    "), k("return"), plain(" err"));
      line(plain("}"));
      line(plain("handle(resp)"), c(" // resp.Allowed, resp.Remaining, resp.ResetAtUnixMs"));
      return L;
    }

    if (tab === "gin") {
      var usesWindow = cfg.algo !== "tokenbucket";
      line(k("import"), plain(" ("));
      line(plain("    "), s("\"net/http\""));
      if (usesWindow) line(plain("    "), s("\"time\""));
      line();
      line(plain("    "), s("\"github.com/gin-gonic/gin\""));
      line();
      line(plain("    "), t("rategateclient"), plain(" "), s("\"github.com/Abolfazlrwm/rategate/pkg/client\""));
      line(plain("    "), t("rategatemw"), plain("     "), s("\"github.com/Abolfazlrwm/rategate/pkg/middleware\""));
      line(plain(")"));
      line();
      line(plain("limiter "), k(":="), plain(" "), t("rategateclient"), plain("."), f("NewHTTPClient"), plain("("), s("\"http://localhost:8080\""), plain(")"));
      line();
      line(plain("r "), k(":="), plain(" "), t("gin"), plain("."), f("Default"), plain("()"));
      line(plain("r."), f("Use"), plain("("), t("rategatemw"), plain("."), f("GinMiddleware"), plain("(limiter, "), t("rategatemw"), plain("."), t("Options"), plain("{"));
      line(plain("    "), t("KeyFunc"), plain(": "), k("func"), plain("(req *"), t("http"), plain("."), t("Request"), plain(") "), t("string"), plain(" {"));
      line(plain("        "), k("return"), plain(" "), s("\"user:\""), plain(" + req.Header."), f("Get"), plain("("), s("\"X-User-Id\""), plain(")"));
      line(plain("    },"));
      line(plain("    "), t("Algorithm"), plain(": "), s(goQuote(cfg.algo)), plain(","));
      line(plain("    "), t("Limit"), plain(":     "), n(String(cfg.limit)), plain(","));
      if (cfg.algo === "tokenbucket") {
        line(plain("    "), t("Refill"), plain(":    "), n(String(cfg.refill)), plain(","), c(" // tokens per second; burst of " + cfg.limit));
      } else {
        var gd = goDuration(w).split(" * ")[1].split(".");
        line(plain("    "), t("Window"), plain(":    "), n(String(goDuration(w).split(" * ")[0])), plain(" * "), t(gd[0]), plain("."), t(gd[1]), plain(","));
      }
      line(plain("    "), c("// FailClosed: true -> 503 when RateGate is unreachable (default: fail open)"));
      line(plain("}))"));
      return L;
    }

    // named policy
    var policy = { name: "api-default", algorithm: cfg.algo, limit: cfg.limit };
    if (cfg.algo === "tokenbucket") policy.refill_per_second = cfg.refill;
    else policy.window_ms = w;
    line(c("# config.yaml — the server seeds its policy registry from this file"));
    line(t("policies"), plain(":"));
    line(plain("  - "), t("name"), plain(": "), s("\"api-default\""));
    line(plain("    "), t("algorithm"), plain(": "), s(cfg.algo));
    line(plain("    "), t("limit"), plain(": "), n(String(cfg.limit)));
    if (cfg.algo === "tokenbucket") {
      line(plain("    "), t("refill_per_second"), plain(": "), n(String(cfg.refill)));
    } else {
      line(plain("    "), t("window"), plain(": "), n(yamlDuration(w)));
    }
    line();
    line(c("# Or create it on a running server (201 Created; 409 if the name exists):"));
    line(plain("curl -i -X POST localhost:8080/v1/admin/policies \\"));
    line(plain("  -H "), s("'Content-Type: application/json'"), plain(" \\"));
    line(plain("  -d "), s(escBash(JSON.stringify(policy))));
    line();
    line(c("# Then check against it by name — no inline policy needed:"));
    line(plain("curl -i -X POST localhost:8080/v1/check \\"));
    line(plain("  -H "), s("'Content-Type: application/json'"), plain(" \\"));
    line(plain("  -d "), s(escBash(JSON.stringify({ key: cfg.key, limit_name: "api-default" }))));
    return L;
  }

  return {
    ALGOS: ALGOS,
    WINDOWS_MS: WINDOWS_MS,
    MAX_FACTOR: MAX_FACTOR,
    SHAPES: SHAPES,
    SCENES: SCENES,
    ALGO_DEFAULTS: ALGO_DEFAULTS,
    defaults: defaults,
    normalize: normalize,
    sanitizeKey: sanitizeKey,
    windowMsOf: windowMsOf,
    policyOf: policyOf,
    sustainOf: sustainOf,
    intensity: intensity,
    deriveKeys: deriveKeys,
    rateFromSlider: rateFromSlider,
    sliderFromRate: sliderFromRate,
    mulberry32: mulberry32,
    poisson: poisson,
    fmtInt: fmtInt,
    fmtNum: fmtNum,
    fmtWindow: fmtWindow,
    fmtDur: fmtDur,
    fmtClock: fmtClock,
    fmtStamp: fmtStamp,
    yamlDuration: yamlDuration,
    goDuration: goDuration,
    algoLabel: algoLabel,
    shapeLabel: shapeLabel,
    runScenario: runScenario,
    toParams: toParams,
    fromParams: fromParams,
    escBash: escBash,
    goQuote: goQuote,
    restBody: restBody,
    grpcBody: grpcBody,
    summary: summary,
    exportCode: exportCode,
  };
});
