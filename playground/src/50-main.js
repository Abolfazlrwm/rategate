/* 50-main.js - wiring: controls, transport, keyboard, sharing, scenarios,
   copy buttons, reveal-on-scroll, the DOM tick and the frame loop.          */

var stageField = new Field($("flow"), {
  nowNs: function () {
    return sim.t;
  },
  decide: decide,
  rate: function () {
    return hasKey() ? cfg.rate * C.intensity(cfg.shape, Number(sim.t / NS_PER_S)) : 0;
  },
  moving: function () {
    return sim.playing;
  },
  layout: "stage",
});

var heroField = new Field($("heroGate"), {
  nowNs: heroNs,
  decide: heroDecide,
  rate: heroRate,
  moving: function () {
    return true;
  },
  layout: "hero",
});

var vis = { hero: true, stage: true };
var stageDirty = true;
var winMemo = { slidingwindow: C.ALGO_DEFAULTS.slidingwindow.windowIdx, adaptive: C.ALGO_DEFAULTS.adaptive.windowIdx };
var SCENE_TITLES = {
  burst: "Launch-day burst",
  quota: "Three per minute",
  spike: "Ride the spike",
  tenants: "Noisy neighbour",
  slow: "Protect a slow endpoint",
  trickle: "Ten calls per second",
};

// ------------------------------------------------------------ plumbing

var toastTimer = 0;
function toast(msg) {
  var t = el("toast");
  t.textContent = msg;
  t.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(function () {
    t.classList.remove("show");
  }, 2000);
}

function setIcon(root, id) {
  var use = root.querySelector("use");
  if (!use) return;
  use.setAttribute("href", "#" + id);
  use.setAttributeNS("http://www.w3.org/1999/xlink", "xlink:href", "#" + id);
}

function setPlaying(on) {
  sim.playing = on;
  var btn = el("btnPlay");
  btn.setAttribute("aria-pressed", on ? "true" : "false");
  btn.title = on ? "Pause the simulation (Space)" : "Resume the simulation (Space)";
  setIcon(btn, on ? "i-pause" : "i-play");
  setText(el("btnPlayText"), on ? "Pause" : "Play");
  stageDirty = true;
}

function syncUrl() {
  try {
    history.replaceState(null, "", "?" + new URLSearchParams(C.toParams(cfg)).toString() + "#playground");
  } catch (e) {
    /* file:// or a sandboxed context: the link simply stays as it is */
  }
}

var urlTimer = 0;
function syncUrlSoon() {
  clearTimeout(urlTimer);
  urlTimer = setTimeout(syncUrl, 400);
}

// The key list changes: forget every key's state and restart the picture.
function rebuildKeys(mark) {
  sim.keys = cfg.key ? C.deriveKeys(cfg.key, cfg.nKeys) : [];
  sim.focus = 0;
  clearState(!!mark);
  stageField.clear();
  stageDirty = true;
}

// A policy change: keep the clock and the chart, drop the key state.
function policyChanged() {
  invalidate();
  clearState(true);
  stageField.clear();
  stageDirty = true;
}

function switchAlgo(algo) {
  if (C.ALGOS.indexOf(algo) < 0 || algo === cfg.algo) return;
  cfg.algo = algo;
  if (algo !== "tokenbucket") cfg.windowIdx = winMemo[algo];
  policyChanged();
  syncControls();
  syncUrl();
}

function loadScene(name) {
  var sc = C.SCENES[name];
  if (!sc) return;
  cfg = C.normalize({
    algo: sc.algo,
    key: sc.key,
    nKeys: sc.keys,
    shape: sc.shape,
    rate: sc.rate,
    speed: sc.speed,
    limit: sc.limit,
    refill: sc.refill,
    windowIdx: sc.windowIdx,
  });
  if (cfg.algo !== "tokenbucket") winMemo[cfg.algo] = cfg.windowIdx;
  sim.skew = sc.skew || 0;
  invalidate();
  sim.keys = C.deriveKeys(cfg.key, cfg.nKeys);
  resetAll();
  stageField.clear();
  setPlaying(true);
  syncControls();
  syncUrl();
  document.getElementById("playground").scrollIntoView({ behavior: reduceMotion ? "auto" : "smooth", block: "start" });
  toast("Loaded: " + (SCENE_TITLES[name] || name));
}

// --------------------------------------------------------------- copy

function flashCopy(btn) {
  if (!btn || btn.__busy) return;
  btn.__busy = true;
  var use = btn.querySelector("use");
  var was = use ? use.getAttribute("href") : null;
  btn.classList.add("copy-done");
  if (use) {
    use.setAttribute("href", "#i-check");
    use.setAttributeNS("http://www.w3.org/1999/xlink", "xlink:href", "#i-check");
  }
  setTimeout(function () {
    btn.classList.remove("copy-done");
    if (use && was) {
      use.setAttribute("href", was);
      use.setAttributeNS("http://www.w3.org/1999/xlink", "xlink:href", was);
    }
    btn.__busy = false;
  }, 1600);
}

function copyText(text, btn, msg) {
  var done = function () {
    flashCopy(btn);
    toast(msg || "Copied to the clipboard");
  };
  var legacy = function () {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.top = "0";
    ta.style.left = "0";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try {
      ok = document.execCommand("copy");
    } catch (e) {
      ok = false;
    }
    document.body.removeChild(ta);
    if (ok) done();
    else toast("Could not copy: select the text and press Ctrl+C");
  };
  if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(done, legacy);
  else legacy();
}

function copyTargetText(target) {
  var text = target.textContent || "";
  if (target.hasAttribute("data-copy-until-blank")) text = text.split(/\r?\n\r?\n/)[0];
  return text;
}

function wireCopy() {
  var btns = document.querySelectorAll("[data-copy]");
  for (var i = 0; i < btns.length; i++) {
    btns[i].addEventListener("click", function (e) {
      var target = $(e.currentTarget.getAttribute("data-copy"));
      if (target) copyText(copyTargetText(target), e.currentTarget, "Command copied");
    });
  }
  el("btnCopyCode").addEventListener("click", function () {
    copyText(el("exportCode").textContent || "", el("btnCopyCode"), "Code copied");
  });
  el("btnShare").addEventListener("click", function () {
    syncUrl();
    copyText(location.href, el("btnShare"), "Link to this setup copied");
  });
}

// ----------------------------------------------------------- scenarios

function sparkSvg(r) {
  var NS = "http://www.w3.org/2000/svg";
  var W = 240;
  var H = 60;
  var n = r.bins.length;
  var max = 1;
  var i;
  for (i = 0; i < n; i++) max = Math.max(max, r.bins[i].ok + r.bins[i].no);
  var X = function (i) {
    return (i / (n - 1)) * W;
  };
  var Y = function (v) {
    return H - 4 - (v / max) * (H - 10);
  };
  var tot = "";
  var ok = "";
  var denied = 0;
  for (i = 0; i < n; i++) {
    var c = i ? "L" : "M";
    tot += c + X(i).toFixed(1) + " " + Y(r.bins[i].ok + r.bins[i].no).toFixed(1);
    ok += c + X(i).toFixed(1) + " " + Y(r.bins[i].ok).toFixed(1);
    denied += r.bins[i].no;
  }
  var back = "";
  for (i = n - 1; i >= 0; i--) back += "L" + X(i).toFixed(1) + " " + Y(r.bins[i].ok).toFixed(1);

  var svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 " + W + " " + H);
  svg.setAttribute("preserveAspectRatio", "none");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("focusable", "false");
  var add = function (d, fill, stroke) {
    var p = document.createElementNS(NS, "path");
    p.setAttribute("d", d);
    p.setAttribute("fill", fill);
    if (stroke) {
      p.setAttribute("stroke", stroke);
      p.setAttribute("stroke-width", "1.2");
      p.setAttribute("stroke-linejoin", "round");
      p.setAttribute("vector-effect", "non-scaling-stroke");
    }
    svg.appendChild(p);
  };
  add(ok + "L" + W + " " + (H - 4) + "L0 " + (H - 4) + "Z", "rgba(52,211,153,0.3)");
  if (denied) add(tot + back + "Z", "rgba(251,113,133,0.28)");
  if (denied) add(tot, "none", "#fb7185");
  add(ok, "none", "#34d399");
  return svg;
}

function buildSparklines() {
  var cards = document.querySelectorAll("[data-scene]");
  for (var i = 0; i < cards.length; i++) {
    var box = cards[i].querySelector(".s-spark");
    var scene = C.SCENES[cards[i].getAttribute("data-scene")];
    if (box && scene) box.replaceChildren(sparkSvg(C.runScenario(A, scene)));
  }
}

function wireScenarios() {
  var cards = document.querySelectorAll("[data-scene]");
  for (var i = 0; i < cards.length; i++) {
    cards[i].addEventListener("click", function (e) {
      loadScene(e.currentTarget.getAttribute("data-scene"));
    });
  }
}

// ------------------------------------------------------------- reveal

function wireReveal() {
  var items = document.querySelectorAll(".reveal");
  var i;
  if (!("IntersectionObserver" in window) || reduceMotion) {
    for (i = 0; i < items.length; i++) items[i].classList.add("in");
    return;
  }
  var io = new IntersectionObserver(function (entries) {
    for (var j = 0; j < entries.length; j++) {
      if (entries[j].isIntersecting) {
        entries[j].target.classList.add("in");
        io.unobserve(entries[j].target);
      }
    }
  }, { rootMargin: "0px 0px -6% 0px" });
  for (i = 0; i < items.length; i++) io.observe(items[i]);
  // safety net: nothing may stay hidden if the observer never fires for it
  setTimeout(function () {
    for (var k = 0; k < items.length; k++) items[k].classList.add("in");
  }, 4000);
}

// ----------------------------------------------------------- controls

function bindSlider(id, apply, isPolicy) {
  var input = el(id);
  input.addEventListener("input", function () {
    apply(Number(input.value));
    setRange(id, Number(input.value));
    invalidate();
    if (isPolicy) policyChanged();
    updateLabels();
    updateNotes();
    renderExport();
    syncUrlSoon();
  });
}

function wireControls() {
  el("algoTabs").addEventListener("click", function (e) {
    var btn = e.target.closest("[data-algo]");
    if (btn) switchAlgo(btn.getAttribute("data-algo"));
  });
  el("speedSeg").addEventListener("click", function (e) {
    var btn = e.target.closest("[data-speed]");
    if (!btn) return;
    cfg.speed = Number(btn.getAttribute("data-speed"));
    setPressed(el("speedSeg"), "data-speed", cfg.speed);
    syncUrlSoon();
  });
  el("keysSeg").addEventListener("click", function (e) {
    var btn = e.target.closest("[data-keys]");
    if (!btn) return;
    cfg.nKeys = Number(btn.getAttribute("data-keys"));
    setPressed(el("keysSeg"), "data-keys", cfg.nKeys);
    rebuildKeys(true);
    syncUrlSoon();
  });
  el("shapeSeg").addEventListener("click", function (e) {
    var btn = e.target.closest("[data-shape]");
    if (!btn) return;
    cfg.shape = btn.getAttribute("data-shape");
    setPressed(el("shapeSeg"), "data-shape", cfg.shape);
    updateNotes();
    syncUrlSoon();
  });

  el("keyInput").addEventListener("input", function () {
    cfg.key = C.sanitizeKey(el("keyInput").value);
    rebuildKeys(true);
    renderExport();
    syncUrlSoon();
  });

  bindSlider("sLimit", function (v) {
    cfg.limit = v;
  }, true);
  bindSlider("sRefill", function (v) {
    cfg.refill = v;
  }, true);
  bindSlider("sWindow", function (v) {
    cfg.windowIdx = v;
    winMemo[cfg.algo] = v;
  }, true);
  bindSlider("sRate", function (v) {
    cfg.rate = C.rateFromSlider(v);
  }, false);

  // clicking a key in the table makes it the one the pictures follow
  el("keysBody").addEventListener("click", function (e) {
    var row = e.target.closest("tr[data-i]");
    if (row) focusRow(Number(row.getAttribute("data-i")));
  });
  el("keysBody").addEventListener("keydown", function (e) {
    if (e.key !== "Enter" && e.key !== " ") return;
    var row = e.target.closest("tr[data-i]");
    if (!row) return;
    e.preventDefault();
    focusRow(Number(row.getAttribute("data-i")));
  });

  el("btnPlay").addEventListener("click", function () {
    setPlaying(!sim.playing);
  });
  el("btnBurst").addEventListener("click", function () {
    sendBurst(10);
  });
  el("btnReset").addEventListener("click", function () {
    resetAll();
    stageField.clear();
    stageDirty = true;
    toast("Simulation reset");
  });

  // export tabs: click plus the arrow keys a tablist is expected to honour
  el("exportTabs").addEventListener("click", function (e) {
    var tab = e.target.closest("[data-tab]");
    if (tab) setExportTab(tab.getAttribute("data-tab"), false);
  });
  el("exportTabs").addEventListener("keydown", function (e) {
    var tabs = Array.prototype.slice.call(el("exportTabs").querySelectorAll("[data-tab]"));
    var at = tabs.indexOf(e.target.closest("[data-tab]"));
    if (at < 0) return;
    var next = -1;
    if (e.key === "ArrowRight") next = (at + 1) % tabs.length;
    else if (e.key === "ArrowLeft") next = (at - 1 + tabs.length) % tabs.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = tabs.length - 1;
    if (next < 0) return;
    e.preventDefault();
    setExportTab(tabs[next].getAttribute("data-tab"), true);
  });

  // the hero gate fires a burst
  var stage = el("heroStage");
  stage.addEventListener("click", function () {
    heroBurst(24);
  });
  stage.addEventListener("keydown", function (e) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      heroBurst(24);
    }
  });
}

function focusRow(i) {
  if (i === sim.focus) return;
  sim.focus = clamp(i, 0, Math.max(0, sim.keys.length - 1));
  sim.last = null;
  sim.keysDirty = true;
  sim.feedDirty = true;
  sim.tokHist.fill(null);
  stageDirty = true;
}

function wireKeyboard() {
  document.addEventListener("keydown", function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    var t = e.target;
    var tag = t && t.tagName;
    if (!tag || tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || tag === "PRE" || tag === "VIDEO" || t.isContentEditable) return;
    if (e.key === " ") {
      if (tag === "BUTTON" || tag === "A" || t.getAttribute("role") === "button" || t.getAttribute("role") === "tab") return;
      e.preventDefault();
      setPlaying(!sim.playing);
    } else if (e.key === "b" || e.key === "B") sendBurst(10);
    else if (e.key === "r" || e.key === "R") {
      resetAll();
      stageField.clear();
      stageDirty = true;
    } else if (e.key === "1") switchAlgo("tokenbucket");
    else if (e.key === "2") switchAlgo("slidingwindow");
    else if (e.key === "3") switchAlgo("adaptive");
  });
}

function wireResize() {
  if (window.ResizeObserver) {
    var ro = new ResizeObserver(function (entries) {
      for (var i = 0; i < entries.length; i++) entries[i].target.__dirty = true;
      stageDirty = true;
    });
    var all = document.querySelectorAll("canvas");
    for (var i = 0; i < all.length; i++) ro.observe(all[i]);
  }
  window.addEventListener("resize", function () {
    markCanvasesDirty();
    stageDirty = true;
  });
  if (mqReduce) {
    var onChange = function () {
      reduceMotion = mqReduce.matches;
      stageField.clear();
      heroField.clear();
      stageDirty = true;
    };
    if (mqReduce.addEventListener) mqReduce.addEventListener("change", onChange);
  }
  document.addEventListener("visibilitychange", function () {
    last = -1; // no jump after the tab comes back
  });
}

function wireVisibility() {
  if (!("IntersectionObserver" in window)) return;
  var io = new IntersectionObserver(function (entries) {
    for (var i = 0; i < entries.length; i++) {
      if (entries[i].target === el("heroGate")) vis.hero = entries[i].isIntersecting;
      else vis.stage = entries[i].isIntersecting;
    }
  }, { rootMargin: "800px" }); // pre-roll while the section scrolls in
  io.observe(el("heroGate"));
  io.observe(el("flow"));
  // if the observer ever misses the section (embeds, odd engines), the first
  // touch on the playground still starts the simulation
  function wake(e) {
    if (e.target && e.target.closest && e.target.closest("#playground")) vis.stage = true;
  }
  document.addEventListener("pointerdown", wake, true);
  document.addEventListener("keydown", wake, true);
}

// --------------------------------------------------------------- tick

function updatePanels() {
  var v = { key: focusKey(), win: null, aim: null };
  if (cfg.algo === "slidingwindow") v.win = windowView(focusKey(), sim.t);
  else if (cfg.algo === "adaptive") v.aim = adaptiveView(focusKey());
  updateKpis();
  updateVizOverlay(v);
  updateWhy(v);
  renderHttp();
  if (sim.feedDirty) {
    sim.feedDirty = false;
    renderFeed();
  }
  if (sim.keysDirty) {
    sim.keysDirty = false;
    renderKeys();
  }
  if (cfg.algo === "tokenbucket") {
    sim.tokHist.push(hasKey() ? projectedTokens(focusKey(), sim.t) : null);
    sim.tokHist.shift();
    stageDirty = true;
  }
}

function chartInsight() {
  var lo = Math.max(0, sim.binAbs - 59);
  var hi = sim.binAbs;
  if (hi < 0) return "Decisions per half-second of simulated time. The chart fills in as the clock runs.";
  var ok = 0;
  var no = 0;
  for (var a = lo; a <= hi; a++) {
    var bin = sim.bins[a % BIN_COUNT];
    ok += bin.ok;
    no += bin.no;
  }
  var tot = ok + no;
  if (!tot) return hasKey() ? "No requests in the last 30 seconds of simulated time." : "Type a rate-limit key to start sending requests.";
  var pct = Math.round((ok / tot) * 100);
  var when = Number(sim.t / NS_PER_S) >= 30 ? "in the last 30 s" : "so far";
  var w = C.fmtWindow(C.windowMsOf(cfg));
  if (cfg.algo === "tokenbucket") {
    if (!no) return "All " + C.fmtInt(ok) + " requests were admitted " + when + ": the bucket refills " + C.fmtNum(cfg.refill) + " tokens a second and never ran dry.";
    return "The bucket refills " + C.fmtNum(cfg.refill) + " tokens a second: " + C.fmtInt(ok) + " of " + C.fmtInt(tot) + " requests (" + pct + "%) were admitted " + when + ".";
  }
  if (cfg.algo === "slidingwindow") {
    if (!no) return "All " + C.fmtInt(ok) + " requests stayed under " + cfg.limit + " per " + w + ". None was denied.";
    return "The window admits " + cfg.limit + " per " + w + ": " + C.fmtInt(ok) + " of " + C.fmtInt(tot) + " requests (" + pct + "%) got through " + when + ".";
  }
  var av = adaptiveView(focusKey());
  var eff = av.state ? Math.trunc(av.state.limit) : av.floor;
  if (!no) return "Every request fits under the current ceiling of " + eff + " (floor " + av.floor + ", cap " + av.cap + "). None was denied.";
  return "The ceiling sits at " + eff + " (floor " + av.floor + ", cap " + av.cap + "): " + C.fmtInt(ok) + " of " + C.fmtInt(tot) + " requests (" + pct + "%) were admitted " + when + ".";
}

var srAcc = 0;
function updateSr(dt) {
  srAcc += dt;
  if (srAcc < 2) return;
  srAcc = 0;
  var tot = sim.ok + sim.no;
  var msg =
    sim.playing ? "Running. " : "Paused. ";
  msg += C.fmtClock(sim.t) + " simulated. " + C.fmtInt(sim.ok) + " allowed, " + C.fmtInt(sim.no) + " denied";
  msg += tot ? " (" + Math.round((sim.ok / tot) * 100) + "% admitted). " : ". ";
  msg += C.algoLabel(cfg.algo) + ", " + C.summary(cfg) + ".";
  setText(el("srSummary"), msg);
}

// --------------------------------------------------------------- loop

var last = -1;
var acc = 1; // update the panels on the very first frame
var titleAcc = 1;
var stageWasOff = false;

function frame(now) {
  try {
    var dt = last < 0 ? 0 : Math.min(0.1, (now - last) / 1000);
    last = now;

    if (vis.hero) {
      heroStep(dt);
      heroField.settle();
      var hf = fitCanvas(el("heroGate"));
      if (hf) {
        heroField.draw(dt);
        heroGauge(hf.ctx, heroField.gate);
      }
    }

    var run = sim.playing && vis.stage && hasKey();
    if (run) stepSim(dt);
    else stageField.settle();

    if (vis.stage && (run || stageDirty || stageField.busy())) {
      stageDirty = false;
      stageField.draw(dt);
      drawViz(run ? dt : 0);
      drawChart();
    }

    acc += dt;
    if (acc >= 0.125) {
      acc = 0;
      updatePanels();
    }
    titleAcc += dt;
    if (titleAcc >= 1) {
      titleAcc = 0;
      setText(el("chartTitle"), chartInsight());
    }
    updateSr(dt);
  } catch (e) {
    // A single bad frame (e.g. a canvas call handed non-finite geometry) must
    // not kill the whole animation loop. Skip this frame and keep ticking so
    // the page never freezes on an edge-case policy value.
    if (typeof console !== "undefined" && console.warn) console.warn("rategate: frame skipped", e && e.message);
  }
  requestAnimationFrame(frame);
}

// --------------------------------------------------------------- start

function init() {
  vizCard = el("viz").closest(".card");
  sim.keys = cfg.key ? C.deriveKeys(cfg.key, cfg.nKeys) : [];
  wireResize();
  wireVisibility();
  wireControls();
  wireKeyboard();
  wireChartPointer();
  wireCopy();
  wireScenarios();
  wireReveal();
  buildSparklines();
  setPlaying(sim.playing);
  syncControls();
  updatePanels();
  setText(el("chartTitle"), chartInsight());
  requestAnimationFrame(frame);
}

init();
