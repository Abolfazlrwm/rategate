#!/usr/bin/env python3
"""check_coverage.py — enforce 100% statement coverage on given packages.

Reads a Go cover profile (go test -coverprofile) and exits non-zero if any
of the named packages has total coverage below 100%.

Usage:
    check_coverage.py coverage.out pkg1 pkg2 ...
"""
import re
import sys
from collections import defaultdict


def main(profile_path: str, required_packages: list[str]) -> int:
    # Map: package (directory, no .go file) -> [covered_stmts, total_stmts].
    # A cover-profile line looks like:
    #   github.com/x/y/pkg/file.go:10.2,15.3 5 3
    # The package is the directory portion (strip the last "/file.go").
    pkg_cov = defaultdict(lambda: [0, 0])
    line_re = re.compile(r"^([^:]+):\d+\.\d+,\d+\.\d+ (\d+) (\d+)$")
    with open(profile_path) as f:
        for line in f:
            line = line.strip()
            if line.startswith("mode:"):
                continue
            m = line_re.match(line)
            if not m:
                continue
            file_path = m.group(1)
            # Strip the trailing "/filename.go" to get the package import path.
            pkg = file_path.rsplit("/", 1)[0]
            stmts = int(m.group(2))
            count = int(m.group(3))
            pkg_cov[pkg][1] += stmts
            if count > 0:
                pkg_cov[pkg][0] += stmts

    failures = []
    for pkg in required_packages:
        cov, total = pkg_cov.get(pkg, [0, 0])
        if total == 0:
            failures.append(f"{pkg}: no statements (package not built?)")
            continue
        pct = 100.0 * cov / total
        if pct < 100.0:
            failures.append(f"{pkg}: {pct:.2f}% (require 100%)")
        else:
            print(f"OK: {pkg} 100.00%")

    if failures:
        print("\nCoverage check FAILED:", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        return 1
    print("\nAll required packages at 100% coverage.")
    return 0


if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("usage: check_coverage.py <profile> <pkg> [<pkg> ...]", file=sys.stderr)
        sys.exit(2)
    sys.exit(main(sys.argv[1], sys.argv[2:]))
