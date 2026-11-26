#!/usr/bin/env bash
# Headless smoke test for the built demo page.
# usage: verify.sh BASE_URL OUT_DIR     (expects a static server already running)
set -u
BASE="${1:-http://127.0.0.1:8765/}"
OUT="${2:-/tmp/render/rg}"
mkdir -p "$OUT"

# agent-browser re-parses eval scripts, so hand it base64 (-b) instead of raw JS
eb() { agent-browser eval -b "$(printf %s "$1" | base64 -w0)"; }

read -r -d '' DIAG <<'EOF'
(function(){
  var c=[].slice.call(document.querySelectorAll("canvas")).map(function(x){
    var k=x.getContext("2d"); if(!k) return {id:x.id,err:"noctx"};
    var d=k.getImageData(0,0,x.width,x.height).data,n=0;
    for(var i=3;i<d.length;i+=4){ if(d[i]) n++; }
    return {id:x.id,w:x.width,h:x.height,lit:n};
  });
  function t(i){var e=document.getElementById(i);return e?e.textContent.trim().slice(0,90):"MISSING:"+i;}
  var cw=document.documentElement.clientWidth;
  function scrollableX(e){for(var a=e.parentElement;a;a=a.parentElement){var o=getComputedStyle(a).overflowX;if(o==="auto"||o==="scroll")return true;}return false;}
  var over=[].slice.call(document.querySelectorAll("body *")).filter(function(e){
    var r=e.getBoundingClientRect();
    return r.width!==0 && r.right>cw+1 && !scrollableX(e);
  }).slice(0,6).map(function(e){return e.tagName+"."+(e.className||"").toString().slice(0,40)+" right="+(e.getBoundingClientRect().right|0);});
  var hid=[].slice.call(document.querySelectorAll(".reveal")).filter(function(e){return !e.classList.contains("in");}).length;
  return JSON.stringify({
    sw:document.documentElement.scrollWidth, cw:cw, over:over,
    canvases:c, revealNotIn:hid,
    play:t("btnPlayText"), offered:t("kOffered"), allowed:t("kAllowed"),
    denied:t("kDenied"), rate:t("kRate"), chart:t("chartTitle"),
    viz:t("vizMain"), vizS1:t("vizS1"), vizS2:t("vizS2"),
    http:t("httpStatus"), feed:document.querySelectorAll("#feedList li").length,
    keys:document.querySelectorAll("#keysBody tr").length,
    heroA:t("heroAllowed"), heroD:t("heroDenied"),
    why:t("whyText").slice(0,80),
    fontInter:document.fonts?document.fonts.check("600 14px Inter"):null,
    fontMono:document.fonts?document.fonts.check("500 12px JetBrains Mono"):null
  });
})()
EOF

probe() { # name width height
  agent-browser set viewport "$2" "$3" >/dev/null 2>&1
  agent-browser open "$BASE" >/dev/null 2>&1
  agent-browser wait 3000 >/dev/null 2>&1
  echo "--- $1 ($2x$3)"
  eb "$DIAG"
  agent-browser screenshot --full "$OUT/$1.png" >/dev/null 2>&1
}

probe desktop 1440 900
probe tablet 820 1180
probe phone 375 812
probe narrow 320 640

echo "--- errors after four loads"
agent-browser errors

echo "--- interactions (desktop)"
agent-browser set viewport 1440 900 >/dev/null 2>&1
agent-browser open "$BASE" >/dev/null 2>&1
agent-browser wait 1200 >/dev/null 2>&1
eb '(function(){function q(s){return document.querySelector(s);}function t(i){var e=document.getElementById(i);return e?e.textContent.trim().slice(0,110):"MISSING:"+i;}
  q("#algoTabs button[data-algo=slidingwindow]").click();
  q("#algoTabs button[data-algo=adaptive]").click();
  return JSON.stringify({pressed:(q("#algoTabs [aria-pressed=true]")||{}).textContent.trim(),vizCard:document.getElementById("viz").closest(".card").className,viz:t("vizMain"),s1:t("vizS1"),s2:t("vizS2"),limitLabel:t("kLimitLabel"),meter:t("meterText")});})()'
agent-browser wait 400 >/dev/null 2>&1
eb '(function(){function t(i){var e=document.getElementById(i);return e?e.textContent.trim().slice(0,110):"MISSING:"+i;}
  document.getElementById("btnBurst").click();
  return JSON.stringify({afterBurst:{offered:t("kOffered"),denied:t("kDenied"),play:t("btnPlayText")},url:location.search.slice(0,120)});})()'
agent-browser wait 600 >/dev/null 2>&1
eb '(function(){function t(i){var e=document.getElementById(i);return e?e.textContent.trim().slice(0,110):"MISSING:"+i;}
  document.getElementById("btnPlay").click();
  var i=document.getElementById("keyInput");i.value="";i.dispatchEvent(new Event("input",{bubbles:true}));
  return JSON.stringify({playAfterPause:t("btnPlayText"),http:t("httpStatus"),httpLen:document.getElementById("httpPre").textContent.length,keysRows:document.querySelectorAll("#keysBody tr").length,firstRow:(document.querySelector("#keysBody tr")||{}).textContent.slice(0,60),why:t("whyText").slice(0,70)});})()'
agent-browser wait 500 >/dev/null 2>&1
eb '(function(){function t(i){var e=document.getElementById(i);return e?e.textContent.trim().slice(0,110):"MISSING:"+i;}
  var i=document.getElementById("keyInput");i.value="api:search";i.dispatchEvent(new Event("input",{bubbles:true}));
  document.getElementById("btnReset").click();
  var tabs=[].slice.call(document.querySelectorAll("#exportTabs button")).map(function(b){b.click();return b.id+":"+(document.getElementById("exportCode").textContent.slice(0,24).replace(/\n/g,"|"));});
  return JSON.stringify({tabs:tabs,shareText:document.getElementById("btnShare").textContent.trim(),feedN:document.querySelectorAll("#feedList li").length});})()'
agent-browser wait 300 >/dev/null 2>&1
echo "--- errors after interactions"
agent-browser errors
ls -la "$OUT"
