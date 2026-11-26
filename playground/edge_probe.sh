#!/usr/bin/env bash
# Edge-case probe: exercise the policy values that used to make the canvas
# math produce NaN -> createLinearGradient throw -> frozen rAF loop.
# Expects a static server already serving the built page at BASE.
set -u
BASE="${1:-http://127.0.0.1:8765/}"
eb() { agent-browser eval -b "$(printf %s "$1" | base64 -w0)"; }
STATE='(function(){function t(i){var e=document.getElementById(i);return e?e.textContent.trim().slice(0,60):"MISSING:"+i;}
var c=[].slice.call(document.querySelectorAll("canvas")).map(function(x){var k=x.getContext("2d");if(!k)return{id:x.id,noctx:1};var d=k.getImageData(0,0,x.width,x.height).data,n=0;for(var i=3;i<d.length;i+=4)if(d[i])n++;return{id:x.id,w:x.width,h:x.height,lit:n};});
return JSON.stringify({play:t("btnPlayText"),offered:t("kOffered"),allowed:t("kAllowed"),denied:t("kDenied"),viz:t("vizMain"),http:t("httpStatus"),canvases:c});})()'

probe() { # label url
  agent-browser set viewport 1440 900 >/dev/null 2>&1
  agent-browser open "$2" >/dev/null 2>&1
  agent-browser wait 2800 >/dev/null 2>&1
  echo "--- $1"
  eb "$STATE"
}

# limit=0 used to make tokens/limit = 0/0 = NaN -> level NaN -> gradient throw.
probe "tokenbucket l=0 r=0" "${BASE}?a=tokenbucket&l=0&r=0&q=20&k=user:1&n=1&s=steady&x=1#playground"
probe "tokenbucket l=0 r=8"  "${BASE}?a=tokenbucket&l=0&r=8&q=20&k=user:1&n=1&s=steady&x=1#playground"
probe "slidingwindow l=0"    "${BASE}?a=slidingwindow&l=0&q=20&k=user:1&n=1&s=steady&x=1&w=5#playground"
probe "adaptive l=0 (cap=0)" "${BASE}?a=adaptive&l=0&q=20&k=user:1&n=1&s=steady&x=1&w=5#playground"
# refill=0 with a live bucket: frac stays finite, but exercise it anyway.
probe "tokenbucket r=0 l=20" "${BASE}?a=tokenbucket&l=20&r=0&q=30&k=user:1&n=1&s=spike&x=2#playground"

echo "--- errors after edge probes (must be empty)"
agent-browser errors

echo "--- tooltip probe"
agent-browser open "${BASE}?a=tokenbucket&l=20&r=8&q=15&k=user:1&n=1&s=spike&x=2#playground" >/dev/null 2>&1
agent-browser wait 3500 >/dev/null 2>&1
# dispatch a synthetic pointermove over the chart plot area, then read the tip.
eb '(function(){var c=document.getElementById("chart");var r=c.getBoundingClientRect();c.dispatchEvent(new PointerEvent("pointermove",{clientX:r.left+120,clientY:r.top+40,bubbles:true}));return JSON.stringify({rectW:Math.round(r.width)});})()'
agent-browser wait 500 >/dev/null 2>&1
eb '(function(){var t=document.getElementById("chartTip");return JSON.stringify({on:t.classList.contains("show"),text:(t.textContent||"").replace(/\s+/g," ").trim().slice(0,90)});})()'
echo "--- errors after tooltip"
agent-browser errors
