#!/usr/bin/env python3
"""Assemble the single-file RateGate demo page.

usage: build.py OUT.html [COPY.html]

Reads src/ and fonts/subset/ next to this script and writes one HTML file
with the fonts, the stylesheet and the script inlined. The Content-Security-
Policy meta tag carries sha256 hashes of the three inline blocks, so the page
only runs what this build produced. The script fails (exit 1) on: a leftover
placeholder, "</script" inside the script, a non-covered glyph in the markup
or the stylesheet, or a DOM id the script looks up but the markup lacks.
"""

import base64
import hashlib
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(HERE, "src")
FONTS = os.path.join(HERE, "fonts", "subset")

APP_FILES = [
    "00-core.js",
    "10-flow.js",
    "20-viz.js",
    "21-chart.js",
    "30-panels.js",
    "40-hero.js",
    "50-main.js",
]

BOOT = 'document.documentElement.classList.add("js")'


def read(*parts):
    with open(os.path.join(*parts), encoding="utf-8") as fh:
        return fh.read()


def sha256(text):
    return base64.b64encode(hashlib.sha256(text.encode("utf-8")).digest()).decode("ascii")


def font_face(family, weight, name):
    data = base64.b64encode(read_bytes(os.path.join(FONTS, name))).decode("ascii")
    return (
        '@font-face{font-family:"%s";font-style:normal;font-weight:%s;'
        "font-display:swap;src:url(data:font/woff2;base64,%s) format(\"woff2\")}"
        % (family, weight, data)
    )


def read_bytes(path):
    with open(path, "rb") as fh:
        return fh.read()


def decode_escapes(text):
    """Turn \\uXXXX and \\u{XXXX} escapes into characters, drop nothing else."""
    text = re.sub(r"\\u\{([0-9a-fA-F]{1,6})\}", lambda m: chr(int(m.group(1), 16)), text)
    return re.sub(r"\\u([0-9a-fA-F]{4})", lambda m: chr(int(m.group(1), 16)), text)


def uncovered(text, covered):
    """Codepoints above U+007E that the subset fonts cannot draw."""
    bad = {}
    for ch in text:
        cp = ord(ch)
        if cp < 0x80 or cp in covered or ch in "\n\r\t":
            continue
        bad[cp] = bad.get(cp, 0) + 1
    return bad


def strip_css_comments(css):
    return re.sub(r"/\*.*?\*/", "", css, flags=re.S)


def strip_js_comments(js):
    # Remove /* */ and // comments without looking inside strings: good
    # enough for a report, the markup and stylesheet are the hard checks.
    out = []
    for line in js.split("\n"):
        i = line.find("//")
        out.append(line if i < 0 else line[:i])
    text = "\n".join(out)
    return re.sub(r"/\*.*?\*/", "", text, flags=re.S)


def main():
    out_path = sys.argv[1] if len(sys.argv) > 1 else ""
    if not out_path:
        sys.exit(__doc__)
    copy_path = sys.argv[2] if len(sys.argv) > 2 else ""

    problems = []

    # ---- script -------------------------------------------------------
    algo = read(SRC, "algorithms.js")
    core = read(SRC, "appcore.js")
    app = "\n".join(read(SRC, name) for name in APP_FILES)
    js = (
        algo.rstrip("\n")
        + "\n\n"
        + core.rstrip("\n")
        + '\n\n(function () {\n"use strict";\n'
        + app
        + "\n})();\n"
    )
    if re.search(r"</script", js, re.I):
        problems.append("the script contains '</script'")

    check_file = "/tmp/rategate_demo_check.js"
    with open(check_file, "w", encoding="utf-8") as fh:
        fh.write(js)
    syntax = subprocess.run(["node", "--check", check_file], capture_output=True, text=True)
    if syntax.returncode != 0:
        print(syntax.stderr)
        problems.append("node --check failed")

    # ---- stylesheet ---------------------------------------------------
    css = (
        "/* Inter and JetBrains Mono, subset to the glyphs this page uses.\n"
        "   Copyright the respective authors, SIL Open Font License 1.1:\n"
        "   https://github.com/rsms/inter  https://github.com/JetBrains/JetBrainsMono */\n"
        + font_face("Inter", "400 800", "inter.woff2")
        + "\n"
        + font_face("JetBrains Mono", "400 700", "jbmono.woff2")
        + "\n"
        + read(SRC, "style.css").rstrip("\n")
        + "\n"
        + read(SRC, "extra.css").rstrip("\n")
        + "\n"
    )
    if re.search(r"</style", css, re.I):
        problems.append("the stylesheet contains '</style'")

    # ---- markup -------------------------------------------------------
    body = read(SRC, "body.html")
    head = read(SRC, "head.html")

    style_block = "\n" + css + "\n"
    script_block = "\n" + js + "\n"
    page = (
        head.replace("{{BOOT_HASH}}", sha256(BOOT))
        .replace("{{JS_HASH}}", sha256(script_block))
        .replace("{{CSS_HASH}}", sha256(style_block))
        .replace("{{CSS}}", css)
        .replace("{{BODY}}", body.rstrip("\n"))
        .replace("{{JS}}", js)
    )
    if re.search(r"\{\{[A-Z_]+\}\}", page):
        problems.append("a {{PLACEHOLDER}} is left in the page")
    if re.search(r"style=\"", body):
        problems.append("an inline style= attribute breaks the style hash")

    # ---- ids the script looks up -------------------------------------
    ids = set(re.findall(r'\bid="([^"]+)"', body))
    wanted = set(re.findall(r'(?:\$|el)\("([^"]+)"\)', js))
    missing = sorted(wanted - ids)
    if missing:
        problems.append("ids used by the script but absent from the markup: " + ", ".join(missing))

    # ---- glyph coverage ----------------------------------------------
    with open(os.path.join(FONTS, "cmap.json"), encoding="utf-8") as fh:
        cmap = json.load(fh)
    covered = set(cmap["inter"]) | set(cmap["jbmono"])
    for label, text, hard in (
        ("body.html", body, True),
        ("style.css", strip_css_comments(css.split("*/", 1)[1]), True),
        ("script", strip_js_comments(decode_escapes(js)), False),
    ):
        bad = uncovered(text, covered)
        if bad:
            shown = " ".join("U+%04X(%s)x%d" % (cp, chr(cp), n) for cp, n in sorted(bad.items()))
            msg = "%s: glyphs outside the font subset: %s" % (label, shown)
            print(("FAIL  " if hard else "note  ") + msg)
            if hard:
                problems.append(msg)
        else:
            print("ok    %s: every glyph is in the subset" % label)

    if problems:
        print("\n".join("FAIL  " + p for p in problems))
        sys.exit(1)

    os.makedirs(os.path.dirname(os.path.abspath(out_path)), exist_ok=True)
    with open(out_path, "w", encoding="utf-8") as fh:
        fh.write(page)
    if copy_path:
        os.makedirs(os.path.dirname(os.path.abspath(copy_path)), exist_ok=True)
        with open(copy_path, "w", encoding="utf-8") as fh:
            fh.write(page)

    size = os.path.getsize(out_path)
    print("ok    node --check")
    print("ok    ids: %d referenced, all present" % len(wanted))
    print("ok    CSP: script %s / %s, style %s" % (sha256(BOOT)[:12], sha256(script_block)[:12], sha256(style_block)[:12]))
    print("wrote  %s (%.1f KB, %d lines)%s" % (out_path, size / 1024, page.count("\n"), " and " + copy_path if copy_path else ""))


if __name__ == "__main__":
    main()
