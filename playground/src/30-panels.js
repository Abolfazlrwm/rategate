/* 30-panels.js - the DOM side of the playground: KPI tiles, the "why" card,
   the wire dump, the decision feed, the keys table, the export tabs and the
   controls. Every value the user types is written with textContent only.    */

var domCache = {};
var exportTab = "curl";

function el(id) {
  return domCache[id] || (domCache[id] = $(id));
}

function node(tag, text, cls) {
  var n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
}

function setClass(n, cls) {
  if (n.__c !== cls) {
    n.__c = cls;
    n.className = cls;
  }
}

function setRange(id, value) {
  var input = el(id);
  var min = Number(input.min);
  var max = Number(input.max);
  input.value = String(value);
  input.style.setProperty("--p", (((Number(input.value) - min) / (max - min)) * 100).toFixed(1) + "%");
}

function b(text) {
  return ["b", String(text)];
}

// A paragraph made of plain strings and ["b", text] emphasis.
function rich(n, parts) {
  var sig = "";
  var i;
  for (i = 0; i < parts.length; i++) sig += (typeof parts[i] === "string" ? parts[i] : "<" + parts[i][1] + ">") + "\u0001";
  if (n.__r === sig) return;
  n.__r = sig;
  var frag = document.createDocumentFragment();
  for (i = 0; i < parts.length; i++) {
    frag.appendChild(typeof parts[i] === "string" ? document.createTextNode(parts[i]) : node("b", parts[i][1]));
  }
  n.replaceChildren(frag);
}

// [lowest, highest] intensity multiplier of each traffic shape
var SHAPE_RANGE = { steady: [1, 1], burst: [0.15, 3.2], ramp: [0.1, 1.9], wave: [0.2, 1.8] };

function offeredSub() {
  var r = SHAPE_RANGE[cfg.shape];
  var speed = cfg.speed + "\u00d7";
  if (cfg.shape === "steady") return "steady traffic \u00b7 " + speed;
  if (cfg.shape === "burst") return "peaks at " + C.fmtNum(cfg.rate * r[1]) + " req/s \u00b7 " + speed;
  return C.fmtNum(cfg.rate * r[0]) + " to " + C.fmtNum(cfg.rate * r[1]) + " req/s \u00b7 " + speed;
}

function trafficParts() {
  var rate = cfg.rate;
  var r = SHAPE_RANGE[cfg.shape];
  if (cfg.shape === "steady") return ["steady at ", b(rate + " req/s")];
  if (cfg.shape === "burst") return ["averaging ", b(rate + " req/s"), " with bursts of ", b(rate * r[1] + " req/s")];
  if (cfg.shape === "ramp") return ["ramping from ", b(C.fmtNum(rate * r[0]) + " req/s"), " up to ", b(C.fmtNum(rate * r[1]) + " req/s")];
  return ["swinging between ", b(C.fmtNum(rate * r[0]) + " req/s"), " and ", b(C.fmtNum(rate * r[1]) + " req/s")];
}

function rateText() {
  var r = SHAPE_RANGE[cfg.shape];
  var peak = cfg.rate * r[1];
  var parts = ["This traffic is "].concat(trafficParts()).concat([". "]);
  var s;
  if (cfg.algo === "adaptive") {
    var lo = C.sustainOf(cfg, cfg.limit);
    var hi = C.sustainOf(cfg, cfg.limit * C.MAX_FACTOR);
    if (cfg.rate * r[0] >= hi) return parts.concat(["The ceiling pays for at most ", b(C.fmtNum(hi) + " req/s"), ", so most of it will be denied."]);
    if (peak <= lo) return parts.concat(["That fits under the ", b(C.fmtNum(lo) + " req/s"), " floor, so every request should get through."]);
    return parts.concat(["The ceiling can pay for ", b(C.fmtNum(lo)), " to ", b(C.fmtNum(hi) + " req/s"), " depending on demand."]);
  }
  s = C.sustainOf(cfg);
  if (peak <= s) return parts.concat(["The policy sustains ", b(C.fmtNum(s) + " req/s"), ", enough for the peaks, so nothing should be denied."]);
  if (cfg.rate <= s) return parts.concat(["The policy sustains ", b(C.fmtNum(s) + " req/s"), ": enough on average, but the peaks are shaved off."]);
  return parts.concat(["The policy sustains ", b(C.fmtNum(s) + " req/s"), ", so in the long run about ", b(Math.round((100 * s) / cfg.rate) + "%"), " of these requests get through."]);
}

function updateNotes() {
  var L = cfg.limit;
  var w = C.windowMsOf(cfg);
  var note;
  if (cfg.algo === "tokenbucket") {
    note = ["A fresh key starts with a full bucket, so it can burst up to ", b(L + " requests"), ", then settles to ", b(C.fmtNum(cfg.refill) + " per second"), "."];
  } else if (cfg.algo === "slidingwindow") {
    note = ["At most ", b(L + " requests"), " in any trailing ", b(C.fmtWindow(w)), ". A new key can spend all ", b(L), " at once, then waits for the oldest to leave the window (", b(C.fmtNum(C.sustainOf(cfg)) + " req/s"), " on average)."];
  } else {
    note = ["The ceiling starts at ", b(L + " per " + C.fmtWindow(w)), " window. It steps up by ", b(Math.max(1, L * 0.25)), " while demand grows and is halved when demand falls, between ", b(L), " and ", b(L * C.MAX_FACTOR), "."];
  }
  rich(el("policyNote"), note);
  rich(el("rateNote"), rateText());
}

function updateLabels() {
  var tb = cfg.algo === "tokenbucket";
  var sw = cfg.algo === "slidingwindow";
  var title = tb ? "Token bucket" : sw ? "Sliding window" : "Adaptive AIMD";
  setText(el("policyFile"), tb ? "tokenbucket.go" : sw ? "slidingwindow.go" : "adaptive.go");
  setText(el("vizTitle"), title);
  setText(el("lLimit"), tb ? "Burst (capacity)" : sw ? "Limit" : "Floor (base limit)");
  setText(el("vLimit"), tb ? cfg.limit + " tokens" : cfg.limit + " per window");
  setText(el("vRefill"), C.fmtNum(cfg.refill) + " tok/s");
  setText(el("lWindow"), sw ? "Window" : "AIMD window");
  setText(el("vWindow"), C.fmtWindow(C.windowMsOf(cfg)));
  setText(el("vRate"), C.fmtNum(cfg.rate) + " req/s");
  el("fRefill").hidden = !tb;
  el("fWindow").hidden = tb;
  el("aimdMeter").hidden = cfg.algo !== "adaptive";
  if (vizCard) vizCard.className = "card viz " + (tb ? "tb" : sw ? "sw" : "aimd");
  setText(el("refLabel"), tb ? "what the bucket refills" : "what the policy sustains");
}

function syncControls() {
  setPressed(el("algoTabs"), "data-algo", cfg.algo);
  setPressed(el("speedSeg"), "data-speed", cfg.speed);
  setPressed(el("keysSeg"), "data-keys", cfg.nKeys);
  setPressed(el("shapeSeg"), "data-shape", cfg.shape);
  var ki = el("keyInput");
  if (ki.value !== cfg.key) ki.value = cfg.key;
  setRange("sLimit", cfg.limit);
  setRange("sRefill", cfg.refill);
  setRange("sWindow", cfg.windowIdx);
  setRange("sRate", C.sliderFromRate(cfg.rate));
  updateLabels();
  updateNotes();
  renderExport();
}

// -------------------------------------------------------------- per tick

function keysLabel() {
  var n = sim.keys.length;
  if (!n) return "no key";
  if (n === 1) return "1 key";
  return n + " keys \u00b7 focus " + focusKey();
}

function updateKpis() {
  var tb = cfg.algo === "tokenbucket";
  var sw = cfg.algo === "slidingwindow";
  var tot = sim.ok + sim.no;
  var pct = tot ? (sim.ok / tot) * 100 : null;

  setText(el("kOffered"), String(cfg.rate));
  setText(el("kOfferedSub"), offeredSub());
  setText(el("kAllowed"), C.fmtInt(sim.ok));
  setText(el("kDenied"), C.fmtInt(sim.no));
  setText(el("kRate"), pct == null ? "\u2014" : Math.round(pct) + "%");
  setPct(el("kRateBar"), pct || 0);

  if (tb) {
    setText(el("kLimitLabel"), "Burst");
    setText(el("kLimit"), String(cfg.limit));
    setText(el("kLimitSub"), "refill " + C.fmtNum(cfg.refill) + " tok/s");
  } else if (sw) {
    setText(el("kLimitLabel"), "Limit");
    setText(el("kLimit"), String(cfg.limit));
    setText(el("kLimitSub"), "per " + C.fmtWindow(C.windowMsOf(cfg)) + " \u00b7 " + C.fmtNum(C.sustainOf(cfg)) + " req/s");
  } else {
    var av = adaptiveView(focusKey());
    setText(el("kLimitLabel"), "Ceiling");
    setText(el("kLimit"), String(av.state ? Math.trunc(av.state.limit) : cfg.limit));
    setText(el("kLimitSub"), "of cap " + av.cap + " \u00b7 floor " + av.floor);
  }

  var inst = hasKey() ? cfg.rate * C.intensity(cfg.shape, Number(sim.t / NS_PER_S)) : 0;
  setText(el("flowIn"), String(Math.round(inst)));
  setText(el("flowInSub"), C.shapeLabel(cfg.shape) + (cfg.speed > 1 ? " \u00b7 " + cfg.speed + "\u00d7" : ""));
  setText(el("flowOkPct"), pct == null ? "\u2014" : Math.round(pct) + "%");
  setText(el("flowNoPct"), pct == null ? "\u2014" : Math.round(100 - pct) + "%");
  setText(el("flowOk"), C.fmtInt(sim.ok));
  setText(el("flowNo"), C.fmtInt(sim.no));
  setText(el("flowBl"), keysLabel());

  setText(el("simClock"), C.fmtClock(sim.t));
  setClass(el("clockDot"), sim.playing ? "dot live" : "dot");
}

function tokText(tokens, limit) {
  if (tokens >= limit - 0.05) return String(limit);
  return String(Math.round(tokens * 10) / 10);
}

function updateVizOverlay(v) {
  var tb = cfg.algo === "tokenbucket";
  var sw = cfg.algo === "slidingwindow";
  var w = C.windowMsOf(cfg);
  var nowAbs = baseNs() + sim.t;
  if (tb) {
    var tokens = hasKey() ? projectedTokens(focusKey(), sim.t) : cfg.limit;
    setText(el("vizMain"), tokText(tokens, cfg.limit));
    setText(el("vizMainUnit"), "/ " + cfg.limit + " tokens");
    setText(el("vizS1"), "refill " + C.fmtNum(cfg.refill) + " tok/s");
    setText(el("vizS2"), tokens >= cfg.limit - 0.05 ? "bucket is full" : "full in " + C.fmtDur(fullInNs(tokens)));
  } else if (sw) {
    var view = v.win;
    setText(el("vizMain"), String(view.sum));
    setText(el("vizMainUnit"), "/ " + cfg.limit + " in the last " + C.fmtWindow(w));
    setText(el("vizS1"), view.g + " buckets \u00d7 " + C.fmtDur(view.bucketDur) + ", oldest on the left");
    setText(el("vizS2"), view.active ? "oldest expires in " + C.fmtDur(BigInt(view.oldestStartNs) + policy().windowNs - nowAbs) : "no requests in the window");
  } else {
    var av = v.aim;
    var st = av.state;
    setText(el("vizMain"), String(st ? Math.trunc(st.limit) : av.floor));
    setText(el("vizMainUnit"), "effective limit \u00b7 cap " + av.cap);
    setText(el("vizS1"), "floor " + av.floor + " \u00b7 step +" + C.fmtNum(av.step));
    setText(el("vizS2"), st ? "window closes in " + C.fmtDur(st.windowStartNs + policy().windowNs - nowAbs) : "first window not started");

    var admitted = st ? st.count : 0;
    var rejected = st ? st.rejects : 0;
    var eff = st ? Math.trunc(st.limit) : av.floor;
    setPct(el("meterFill"), (admitted / av.cap) * 100);
    var capEl = el("meterCap");
    var capPos = clamp((eff / av.cap) * 100, 0, 100).toFixed(1) + "%";
    if (capEl.__l !== capPos) {
      capEl.__l = capPos;
      capEl.style.left = capPos;
    }
    setText(el("meterText"), admitted + " of " + eff + " admitted in this window" + (rejected ? " \u00b7 " + rejected + " denied" : ""));
    setText(el("meterNext"), st ? "closes in " + C.fmtDur(st.windowStartNs + policy().windowNs - nowAbs) : "waiting for a request");
  }
  setText(el("vizHint"), v.key || "no key");
}

// ns until the bucket is full again
function fullInNs(tokens) {
  var sec = (cfg.limit - tokens) / cfg.refill;
  return sec > 0 ? BigInt(Math.round(sec * 1e9)) : 0n;
}

function setMeta(l1, v1, l2, v2) {
  setText(el("whyM1Label"), l1);
  setText(el("whyM1"), v1);
  setText(el("whyM2Label"), l2);
  setText(el("whyM2"), v2);
}

function verdict(kind, text, code) {
  setClass(el("whyVerdict"), "v" + (kind ? " " + kind : ""));
  setText(el("whyVerdict"), text);
  setClass(el("whyCode"), "code" + (kind ? " " + kind : ""));
  setText(el("whyCode"), code);
}

function updateWhy(v) {
  var last = sim.last;
  setText(el("whyKey"), v.key || "\u2014");
  var bar = el("whyBar");
  var fill = bar.firstElementChild;
  var win = C.fmtWindow(C.windowMsOf(cfg));
  var ex;
  var d;

  if (!last) {
    verdict("", hasKey() ? "Waiting" : "No key", "\u2014");
    rich(el("whyText"), [hasKey() ? "The next request for this key is explained here, straight from the algorithm. Send one with Send \u00d710 or let the traffic run." : "Type a rate-limit key on the left to start the simulation."]);
    setPct(fill, 0);
    bar.classList.remove("no");
    if (cfg.algo === "tokenbucket") setMeta("Bucket full in", "\u2014", "Retry-After", "\u2014");
    else if (cfg.algo === "slidingwindow") setMeta("In window", "\u2014", "Oldest expires", "\u2014");
    else setMeta("Ceiling", "\u2014", "Window closes", "\u2014");
    return;
  }

  d = last.d;
  ex = last.ex;
  bar.classList.toggle("no", !d.allowed);

  if (cfg.algo === "tokenbucket") {
    var at = ex ? ex.tokensAtDecision : 0;
    var after = ex ? ex.tokensAfter : d.remaining;
    var full = after >= cfg.limit - 0.05 ? "now (full)" : C.fmtDur(fullInNs(after));
    if (d.allowed) {
      verdict("ok", "Allowed", "200 OK");
      rich(el("whyText"), ["The bucket held ", b(C.fmtNum(at)), " tokens for this request, so it spent one and has ", b(tokText(after, cfg.limit)), " left. It refills at ", b(C.fmtNum(cfg.refill)), " tokens a second, up to ", b(cfg.limit), "."]);
      setPct(fill, (after / cfg.limit) * 100);
      setMeta("Bucket full in", full, "Retry-After", "not needed");
    } else {
      verdict("no", "Denied", "429");
      rich(el("whyText"), ["Only ", b(C.fmtNum(at)), " tokens were in the bucket and a request costs a whole one. At ", b(C.fmtNum(cfg.refill)), " tokens a second the next request can pass in ", b(C.fmtDur(d.retryAfterNs)), "."]);
      setPct(fill, (after / cfg.limit) * 100);
      setMeta("Retry-After", C.fmtDur(d.retryAfterNs), "Bucket full in", full);
    }
  } else if (cfg.algo === "slidingwindow") {
    var before = ex ? ex.sumBefore : 0;
    if (d.allowed) {
      verdict("ok", "Allowed", "200 OK");
      rich(el("whyText"), [b(before), " requests were already counted in the trailing ", b(win), ", under the limit of ", b(cfg.limit), ", so this one is admitted as number ", b(before + 1), "."]);
      setPct(fill, ((before + 1) / cfg.limit) * 100);
      var oldest = v.win.active ? C.fmtDur(BigInt(v.win.oldestStartNs) + policy().windowNs - (baseNs() + sim.t)) : "\u2014";
      setMeta("In window", before + 1 + " / " + cfg.limit, "Oldest expires", oldest);
    } else {
      verdict("no", "Denied", "429");
      rich(el("whyText"), [b(before), " requests are already inside the trailing ", b(win), ", which is the limit. The oldest leaves the window in ", b(C.fmtDur(d.retryAfterNs)), ", and that is the Retry-After the client gets."]);
      setPct(fill, 100);
      setMeta("In window", before + " / " + cfg.limit, "Retry-After", C.fmtDur(d.retryAfterNs));
    }
  } else {
    var eff = ex ? ex.effLimit : d.limit;
    var admitted = ex ? ex.admittedThisWindow : 0;
    var rejected = ex ? ex.rejectedThisWindow : 0;
    var cap = cfg.limit * C.MAX_FACTOR;
    if (d.allowed) {
      verdict("ok", "Allowed", "200 OK");
      rich(el("whyText"), [b(admitted - 1), " of the ", b(eff), " requests this ", b(win), " window allows were used, so this one is admitted as number ", b(admitted), "."]);
      setPct(fill, (admitted / eff) * 100);
      setMeta("Ceiling", eff + " \u00b7 cap " + cap, "Window closes in", C.fmtDur(d.resetAtNs - (baseNs() + last.t)));
    } else {
      verdict("no", "Denied", "429");
      rich(el("whyText"), ["The window's ceiling of ", b(eff), " is used up: ", b(admitted), " admitted, ", b(rejected), " turned away. It closes in ", b(C.fmtDur(d.retryAfterNs)), ", and the next ceiling moves by what happened in this one."]);
      setPct(fill, 100);
      setMeta("Admitted", admitted + " / " + eff, "Window closes in", C.fmtDur(d.retryAfterNs));
    }
  }
}

// ------------------------------------------------------------ the wire

function jsonNodes(text) {
  var out = [];
  var re = /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|true|false|null/g;
  var m;
  var last = 0;
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(document.createTextNode(text.slice(last, m.index)));
    last = re.lastIndex;
    if (m[2]) out.push(node("span", m[1], "jk"), document.createTextNode(m[2]));
    else out.push(node("span", m[0], /^(true|false|-?\d)/.test(m[0]) ? "jb" : "jv"));
  }
  if (last < text.length) out.push(document.createTextNode(text.slice(last)));
  return out;
}

// Appends text / nodes followed by a newline.
function lineBuilder(frag) {
  return function () {
    for (var i = 0; i < arguments.length; i++) frag.appendChild(typeof arguments[i] === "string" ? document.createTextNode(arguments[i]) : arguments[i]);
    frag.appendChild(document.createTextNode("\n"));
  };
}

function renderHttp() {
  var last = sim.last;
  var sig = (last ? String(last.t) + "|" + last.k : "-") + "|" + C.restBody(cfg) + "|" + C.windowMsOf(cfg) + "|" + hasKey();
  var pre = el("httpPre");
  if (pre.__s === sig) return;
  pre.__s = sig;
  var frag = document.createDocumentFragment();
  var line = lineBuilder(frag);
  var head = function (name, value) {
    line(node("span", name, "hk"), value);
  };

  line("POST /v1/check HTTP/1.1");
  head("Host: ", "localhost:8080");
  head("Content-Type: ", "application/json");
  line();
  line.apply(null, jsonNodes(C.restBody(cfg)));

  // An empty key never reaches an algorithm: the request fails validation.
  if (!hasKey()) {
    setClass(el("httpStatus"), "st no");
    setText(el("httpStatus"), "400 Bad Request");
    line();
    line("HTTP/1.1 400 Bad Request");
    head("Content-Type: ", "application/json; charset=utf-8");
    line();
    line.apply(null, jsonNodes(JSON.stringify({ error: "Key: 'checkRequest.Key' Error:Field validation for 'Key' failed on the 'required' tag" })));
    pre.replaceChildren(frag);
    return;
  }

  if (!last) {
    setClass(el("httpStatus"), "st");
    setText(el("httpStatus"), "waiting");
    pre.replaceChildren(frag);
    return;
  }

  var d = last.d;
  var status = d.allowed ? "200 OK" : "429 Too Many Requests";
  setClass(el("httpStatus"), d.allowed ? "st ok" : "st no");
  setText(el("httpStatus"), status);
  var resetMs = Number(d.resetAtNs / 1000000n);
  var body = {
    allowed: d.allowed,
    remaining: d.remaining,
    limit: d.limit,
    reset_at_unix_ms: resetMs,
    retry_after_ms: Number(d.retryAfterNs / 1000000n),
    algorithm: cfg.algo,
  };
  line();
  // Go writes response headers in sorted key order
  line("HTTP/1.1 " + status);
  head("Content-Type: ", "application/json; charset=utf-8");
  if (!d.allowed) head("Retry-After: ", String(Math.max(1, Math.ceil(Number(d.retryAfterNs) / 1e9))));
  head("X-RateLimit-Limit: ", String(d.limit));
  head("X-RateLimit-Remaining: ", String(d.remaining));
  head("X-RateLimit-Reset: ", String(Math.floor(resetMs / 1000)));
  line();
  line.apply(null, jsonNodes(JSON.stringify(body)));
  pre.replaceChildren(frag);
}

// ------------------------------------------------------ feed and keys

function replayAnim(n) {
  n.style.animation = "none";
  void n.offsetWidth;
  n.style.animation = "";
}

function renderFeed() {
  var ul = el("feedList");
  var items = sim.feed.slice(0, 8);
  var i;
  if (!items.length) {
    if (!ul.__empty) {
      ul.__empty = true;
      ul.__top = null;
      ul.replaceChildren(node("li", hasKey() ? "Waiting for the first request." : "Type a key to start.", "empty"));
    }
    return;
  }
  ul.__empty = false;
  var rows = ul.__rows || (ul.__rows = []);
  while (rows.length < items.length) {
    var li = document.createElement("li");
    li.appendChild(node("span", "", "k"));
    li.appendChild(node("span", "", "s"));
    li.appendChild(node("span", "", "r"));
    li.appendChild(node("span", "", "t"));
    rows.push(li);
  }
  while (rows.length > items.length) rows.pop().remove();
  var fresh = items[0] !== ul.__top;
  for (i = 0; i < items.length; i++) {
    var r = rows[i];
    var it = items[i];
    var c = r.children;
    c[0].textContent = it.k;
    c[1].className = "s " + (it.ok ? "ok" : "no");
    c[1].textContent = it.ok ? "ALLOW" : "DENY";
    c[2].textContent = "left " + it.left;
    c[3].textContent = C.fmtStamp(it.t);
    r.title = it.ok ? "Admitted" : "Denied";
    if (!r.parentNode) ul.appendChild(r);
  }
  ul.__top = items[0];
  if (fresh && !reduceMotion) replayAnim(rows[0]);
}

function renderKeys() {
  var tb = el("keysBody");
  var keys = sim.keys;
  var i;
  if (!keys.length) {
    if (!tb.__none) {
      tb.__none = true;
      tb.__rows = null;
      var tr = document.createElement("tr");
      tr.className = "none";
      var td = node("td", "Type a key to start.");
      td.colSpan = 4;
      tr.appendChild(td);
      tb.replaceChildren(tr);
    }
    return;
  }
  tb.__none = false;
  var rows = tb.__rows || (tb.__rows = []);
  while (rows.length < keys.length) {
    var r = document.createElement("tr");
    r.tabIndex = 0;
    var k = document.createElement("td");
    k.appendChild(node("span", "", "focus-dot"));
    k.appendChild(document.createTextNode(""));
    r.appendChild(k);
    r.appendChild(node("td", "", "n"));
    r.appendChild(node("td", "", "n"));
    r.appendChild(node("td", "", "n"));
    rows.push(r);
  }
  while (rows.length > keys.length) rows.pop().remove();
  for (i = 0; i < keys.length; i++) {
    var row = rows[i];
    var st = sim.stats.get(keys[i]) || { ok: 0, no: 0, left: null };
    var cells = row.children;
    var focused = i === sim.focus;
    cells[0].lastChild.textContent = keys[i];
    cells[1].textContent = C.fmtInt(st.ok);
    cells[2].textContent = C.fmtInt(st.no);
    cells[3].textContent = st.left == null ? "\u2014" : C.fmtInt(st.left);
    row.className = focused ? "focus" : "";
    row.setAttribute("data-i", String(i));
    row.setAttribute("aria-label", "Show " + keys[i] + (focused ? ", selected" : ""));
    if (!row.parentNode) tb.appendChild(row);
  }
}

// -------------------------------------------------------------- export

function renderExport() {
  var pre = el("exportCode");
  var sl = pre.scrollLeft;
  var stp = pre.scrollTop;
  var lines = C.exportCode(cfg, exportTab);
  var frag = document.createDocumentFragment();
  for (var i = 0; i < lines.length; i++) {
    var toks = lines[i];
    for (var j = 0; j < toks.length; j++) {
      if (toks[j][0]) frag.appendChild(node("span", toks[j][1], toks[j][0]));
      else frag.appendChild(document.createTextNode(toks[j][1]));
    }
    if (i < lines.length - 1) frag.appendChild(document.createTextNode("\n"));
  }
  pre.replaceChildren(frag);
  pre.scrollLeft = sl;
  pre.scrollTop = stp;
  pre.setAttribute("aria-labelledby", "tab-" + exportTab);
}

function setExportTab(name, focus) {
  exportTab = name;
  var tabs = el("exportTabs").querySelectorAll("[data-tab]");
  for (var i = 0; i < tabs.length; i++) {
    var on = tabs[i].getAttribute("data-tab") === name;
    tabs[i].setAttribute("aria-selected", on ? "true" : "false");
    tabs[i].tabIndex = on ? 0 : -1;
    if (on && focus) tabs[i].focus();
  }
  renderExport();
}
