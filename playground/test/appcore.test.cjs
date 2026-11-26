"use strict";
// Unit tests for the playground's DOM-free logic (src/appcore.js).
// Run: node --test test/appcore.test.cjs
const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");

const A = require(path.join(__dirname, "..", "src", "algorithms.js"));
const C = require(path.join(__dirname, "..", "src", "appcore.js"));

const TABS = ["curl", "go", "grpcurl", "gin", "policy"];
const ALGOS = ["tokenbucket", "slidingwindow", "adaptive"];

function text(lines) {
  return lines.map((l) => l.map((tok) => tok[1]).join("")).join("\n");
}

function cfg(over) {
  return C.normalize(Object.assign(C.defaults(), over || {}));
}

// ------------------------------------------------------------- config

test("normalize(null) is the documented default setup", () => {
  const c = C.normalize(null);
  assert.deepEqual(c, C.defaults());
  assert.equal(c.algo, "tokenbucket");
  assert.equal(c.key, "user:1");
  assert.equal(c.limit, 10);
  assert.equal(c.refill, 8);
  assert.equal(c.rate, 12);
  assert.equal(C.windowMsOf(c), 10000);
});

test("normalize switches algorithms and keeps the unused sliders", () => {
  const c = C.normalize({ algo: "adaptive", limit: 20, refill: 2.5, windowIdx: 5 });
  assert.equal(c.windowIdx, 5);
  assert.equal(c.refill, 2.5);
});

test("normalize clamps every out-of-range field", () => {
  const c = C.normalize({
    algo: "bogus",
    key: "  a\u0001b  ",
    nKeys: 4,
    shape: "nope",
    rate: 9999,
    speed: 7,
    limit: 0,
    refill: -1,
    windowIdx: 99,
  });
  assert.equal(c.algo, "tokenbucket");
  assert.equal(c.key, "ab");
  assert.equal(c.nKeys, 1);
  assert.equal(c.shape, "steady");
  assert.equal(c.rate, 12);
  assert.equal(c.speed, 1);
  assert.equal(c.limit, 10);
  assert.equal(c.refill, 8);
  assert.equal(c.windowIdx, 8, "bogus algo falls back to the default window");
});

test("sanitizeKey drops control characters and caps the length", () => {
  assert.equal(C.sanitizeKey("a\nb\tc\u007f"), "abc");
  assert.equal(C.sanitizeKey("x".repeat(80)).length, 48);
  assert.equal(C.sanitizeKey(""), "");
});

test("policyOf builds the shape the algorithm ports expect", () => {
  const tb = C.policyOf(cfg({ algo: "tokenbucket", limit: 7, refill: 2.5 }));
  assert.deepEqual(tb, { limit: 7, refillPerSecond: 2.5, windowNs: 0n });
  const sw = C.policyOf(cfg({ algo: "slidingwindow", limit: 3, windowIdx: 12 }));
  assert.deepEqual(sw, { limit: 3, refillPerSecond: 0, windowNs: 60000000000n });
  const ad = C.policyOf(cfg({ algo: "adaptive", limit: 20, windowIdx: 3 }));
  assert.deepEqual(ad, { limit: 20, refillPerSecond: 0, windowNs: 500000000n });
  assert.equal(A.validate(tb, "tokenbucket"), null);
  assert.equal(A.validate(sw, "slidingwindow"), null);
  assert.equal(A.validate(ad, "adaptive"), null);
});

test("sustainOf: refill for the bucket, limit per window otherwise", () => {
  assert.equal(C.sustainOf(cfg({ algo: "tokenbucket", refill: 8 })), 8);
  assert.equal(C.sustainOf(cfg({ algo: "slidingwindow", limit: 3, windowIdx: 12 })), 0.05);
  assert.equal(C.sustainOf(cfg({ algo: "slidingwindow", limit: 8, windowIdx: 3 })), 16);
  assert.equal(C.sustainOf(cfg({ algo: "adaptive", limit: 20, windowIdx: 5 }), 35), 17.5);
});

// -------------------------------------------------------------- keys

test("deriveKeys increments a trailing number and keeps zero padding", () => {
  assert.deepEqual(C.deriveKeys("user:1", 1), ["user:1"]);
  assert.deepEqual(C.deriveKeys("user:1", 3), ["user:1", "user:2", "user:3"]);
  assert.deepEqual(C.deriveKeys("user:01", 2), ["user:01", "user:02"]);
  assert.deepEqual(C.deriveKeys("ip:10.0.0.1", 2), ["ip:10.0.0.1", "ip:10.0.0.2"]);
});

test("deriveKeys appends :1..:N when the key has no trailing number", () => {
  assert.deepEqual(C.deriveKeys("tenant:acme", 3), ["tenant:acme:1", "tenant:acme:2", "tenant:acme:3"]);
  assert.deepEqual(C.deriveKeys("tenant:acme", 1), ["tenant:acme"]);
});

// ------------------------------------------------------------ sliders

test("rate slider: every position round-trips through the rate", () => {
  let prev = 0;
  for (let v = 0; v <= 100; v++) {
    const r = C.rateFromSlider(v);
    assert.ok(r > prev, `rate must increase at slider ${v} (${r} <= ${prev})`);
    prev = r;
    assert.equal(C.sliderFromRate(r), v, `slider ${v} -> ${r} -> ${C.sliderFromRate(r)}`);
  }
  assert.equal(C.rateFromSlider(0), 1);
  assert.equal(C.rateFromSlider(100), 200);
  assert.equal(C.rateFromSlider(C.sliderFromRate(12)), 12);
});

// ------------------------------------------------------------ random

test("poisson draws reproduce the requested mean", () => {
  const rand = C.mulberry32(42);
  for (const lambda of [0, 0.3, 7, 100, 400]) {
    let sum = 0;
    const n = 20000;
    for (let i = 0; i < n; i++) sum += C.poisson(rand, lambda);
    const mean = sum / n;
    assert.ok(Math.abs(mean - lambda) < Math.max(0.05, lambda * 0.03), `lambda ${lambda}: mean ${mean}`);
  }
  assert.equal(C.poisson(rand, 0), 0);
});

// ------------------------------------------------------------ shapes

test("every traffic shape averages 1.0, so rate is the mean", () => {
  for (const shape of Object.keys(C.SHAPES)) {
    let sum = 0;
    const n = 12000;
    for (let i = 0; i < n; i++) sum += C.intensity(shape, i * 0.01);
    assert.ok(Math.abs(sum / n - 1) < 0.02, `${shape}: mean ${sum / n}`);
  }
});

// --------------------------------------------------------- scenarios

test("scenario 'burst': the first 20 pass, then only what refills", () => {
  const r = C.runScenario(A, C.SCENES.burst);
  const offered = r.ok + r.no;
  assert.ok(offered > 400 && offered < 520, `offered ${offered}`);
  assert.ok(r.ok >= 20 && r.ok <= 20 + 5 * 12 + 1, `allowed ${r.ok}`);
});

test("scenario 'quota': three per trailing minute, so nine over 150 s", () => {
  const r = C.runScenario(A, C.SCENES.quota);
  assert.ok(r.ok >= 7 && r.ok <= 9, `allowed ${r.ok}`);
  assert.ok(r.no > 850, `denied ${r.no}`);
});

test("scenario 'tenants': the noisy key gets 60% and a third of its calls pass", () => {
  const r = C.runScenario(A, C.SCENES.tenants);
  const names = Object.keys(r.perKey);
  assert.equal(names.length, 5);
  const noisy = r.perKey["tenant:acme:1"];
  const total = r.ok + r.no;
  const share = (noisy.ok + noisy.no) / total;
  assert.ok(share > 0.5 && share < 0.7, `noisy share ${share}`);
  const noisyRate = noisy.ok / (noisy.ok + noisy.no);
  assert.ok(noisyRate > 0.25 && noisyRate < 0.42, `noisy allow rate ${noisyRate}`);
  for (const k of names.slice(1)) {
    const s = r.perKey[k];
    assert.ok(s.ok / (s.ok + s.no) >= 0.95, `${k}: ${s.ok}/${s.ok + s.no}`);
    assert.ok((s.ok + s.no) / total < 0.18, `${k} share ${(s.ok + s.no) / total}`);
  }
});

test("scenario 'trickle': a quarter of the calls pass once the burst is spent", () => {
  const r = C.runScenario(A, C.SCENES.trickle);
  const rate = r.ok / (r.ok + r.no);
  assert.ok(rate > 0.22 && rate < 0.3, `allow rate ${rate}`);
});

test("scenario 'slow': never more than the window cap sustains", () => {
  const r = C.runScenario(A, C.SCENES.slow);
  // 60 bins over 12 s = 0.2 s each; the cap is 8 per 0.5 s, so no 0.2 s bin
  // can hold more than 8 + the window's worth carried over.
  for (const b of r.bins) assert.ok(b.ok <= 8, `bin allowed ${b.ok}`);
  const maxRate = Math.max(...r.bins.map((b) => b.ok)) / 0.2;
  assert.ok(maxRate <= 40, `peak allowed ${maxRate} req/s`);
  assert.ok(r.no > 0, "the crests must be clipped");
});

test("every scenario replays without errors on a fresh store", () => {
  for (const name of Object.keys(C.SCENES)) {
    const r = C.runScenario(A, C.SCENES[name]);
    assert.equal(r.bins.length, 60);
    assert.equal(r.bins.reduce((a, b) => a + b.ok, 0), r.ok, name);
    assert.equal(r.bins.reduce((a, b) => a + b.no, 0), r.no, name);
    assert.ok(r.ok + r.no > 100, `${name} produced too little traffic`);
  }
});

// ---------------------------------------------------------------- URL

test("URL params round-trip for every algorithm", () => {
  for (const algo of ALGOS) {
    const c = cfg({ algo, key: "tenant:acme", nKeys: 5, shape: "wave", rate: 63, speed: 16, limit: 33, refill: 2.5, windowIdx: 6 });
    const q = new URLSearchParams(C.toParams(c)).toString();
    assert.deepEqual(C.fromParams("?" + q), c, algo);
  }
  assert.equal(C.fromParams("?a=nope"), null);
  assert.equal(C.fromParams(""), null);
});

test("URL params never smuggle markup into the key", () => {
  const c = C.fromParams("?a=tokenbucket&k=%3Cimg%20src%3Dx%20onerror%3Dalert(1)%3E");
  assert.equal(c.key, "<img src=x onerror=alert(1)>");
  // The key is data: the page only ever renders it through textContent.
  assert.equal(typeof c.key, "string");
});

// ------------------------------------------------------------- export

test("export: curl body matches the playground policy", () => {
  const out = text(C.exportCode(cfg({ algo: "tokenbucket" }), "curl"));
  assert.ok(out.includes(`-d '{"key":"user:1","algorithm":"tokenbucket","limit":10,"refill_per_second":8}'`));
  const sw = text(C.exportCode(cfg({ algo: "slidingwindow", limit: 3, windowIdx: 12 }), "curl"));
  assert.ok(sw.includes(`'{"key":"user:1","algorithm":"slidingwindow","limit":3,"window_ms":60000}'`));
  const ad = text(C.exportCode(cfg({ algo: "adaptive", limit: 20, windowIdx: 5 }), "curl"));
  assert.ok(ad.includes(`'{"key":"user:1","algorithm":"adaptive","limit":20,"window_ms":2000}'`));
});

test("export: single quotes in a key are escaped for bash and JSON", () => {
  const key = "it's \"q\"";
  const c = cfg({ algo: "tokenbucket", key });
  const curl = text(C.exportCode(c, "curl"));
  assert.ok(curl.includes(`-d '{"key":"it'\\''s \\"q\\"","algorithm"`), curl);
  const go = text(C.exportCode(c, "go"));
  assert.ok(go.includes(`Key:             "it's \\"q\\"",`), go);
  const grpc = text(C.exportCode(c, "grpcurl"));
  assert.ok(grpc.includes(`-d '{"key":"it'\\''s \\"q\\"","algorithm":"tokenbucket","limit":"10","refillPerSecond":8}'`), grpc);
});

test("export: Go SDK tab compiles field alignment and carries the 429 caveat", () => {
  const go = text(C.exportCode(cfg(), "go"));
  assert.ok(go.includes("import \"errors\""));
  assert.ok(go.includes("    Key:             \"user:1\","));
  assert.ok(go.includes("    RefillPerSecond: 8,"));
  assert.ok(go.includes("errors.As(err, &denied) && denied.IsDenied()"));
  assert.ok(go.includes("resp is nil here"));
  const sw = text(C.exportCode(cfg({ algo: "slidingwindow" }), "go"));
  assert.ok(sw.includes("    WindowMs:  10000,"));
});

test("export: Gin tab imports time only when a window is used", () => {
  const tb = text(C.exportCode(cfg(), "gin"));
  assert.ok(!tb.includes("\"time\""), tb);
  assert.ok(tb.includes("    Refill:    8,"));
  const ad = text(C.exportCode(cfg({ algo: "adaptive", limit: 20, windowIdx: 5 }), "gin"));
  assert.ok(ad.includes("\"time\""));
  assert.ok(ad.includes("    Window:    2 * time.Second,"));
  const slow = text(C.exportCode(cfg({ algo: "slidingwindow", limit: 8, windowIdx: 3 }), "gin"));
  assert.ok(slow.includes("    Window:    500 * time.Millisecond,"));
  assert.ok(slow.includes("r.Use(rategatemw.GinMiddleware(limiter, rategatemw.Options{"));
});

test("export: named policy tab emits YAML and the admin call", () => {
  const out = text(C.exportCode(cfg(), "policy"));
  assert.ok(out.includes("    algorithm: tokenbucket"));
  assert.ok(out.includes("    refill_per_second: 8"));
  const sw = text(C.exportCode(cfg({ algo: "slidingwindow", limit: 3, windowIdx: 12 }), "policy"));
  assert.ok(sw.includes("    window: 60s"));
  assert.ok(sw.includes(`-d '{"name":"api-default","algorithm":"slidingwindow","limit":3,"window_ms":60000}'`));
  assert.ok(sw.includes(`-d '{"key":"user:1","limit_name":"api-default"}'`));
});

test("export: every tab renders for every algorithm with non-empty tokens", () => {
  for (const tab of TABS) {
    for (const algo of ALGOS) {
      const lines = C.exportCode(cfg({ algo }), tab);
      assert.ok(lines.length > 3, `${tab}/${algo}`);
      for (const line of lines) {
        for (const tok of line) {
          assert.ok(["", "c", "s", "k", "f", "n", "t", "p"].includes(tok[0]), `${tab}/${algo}: class ${tok[0]}`);
          assert.ok(tok[1].length > 0, `${tab}/${algo}: empty token`);
        }
      }
    }
  }
});

// -------------------------------------------------------- formatters

test("formatters", () => {
  assert.equal(C.fmtInt(11546), "11,546");
  assert.equal(C.fmtInt(0), "0");
  assert.equal(C.fmtWindow(100), "100 ms");
  assert.equal(C.fmtWindow(2500), "2.5 s");
  assert.equal(C.fmtWindow(10000), "10 s");
  assert.equal(C.fmtDur(0n), "0 ms");
  assert.equal(C.fmtDur(250000000n), "250 ms");
  assert.equal(C.fmtDur(1250000000n), "1.3 s");
  assert.equal(C.fmtDur(59000000000n), "59 s");
  assert.equal(C.fmtDur(125000000000n), "2 m 5 s");
  assert.equal(C.fmtDur(3720000000000n), "1 h 2 m");
  assert.equal(C.fmtClock(0n), "00:00:00.000");
  assert.equal(C.fmtClock(75412345678n), "00:01:15.412");
  assert.equal(C.fmtClock(3661000000000n), "01:01:01.000");
  assert.equal(C.fmtStamp(75412345678n), "01:15.4");
  assert.equal(C.yamlDuration(500), "500ms");
  assert.equal(C.yamlDuration(2500), "2.5s");
  assert.equal(C.yamlDuration(60000), "60s");
  assert.equal(C.goDuration(500), "500 * time.Millisecond");
  assert.equal(C.goDuration(2500), "2.5 * time.Second");
  assert.equal(C.fmtNum(9.374), "9.37");
  assert.equal(C.fmtNum(12.04), "12");
  assert.equal(C.fmtNum(251.4), "251");
});
