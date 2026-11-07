#!/usr/bin/env python3
"""Benchmark the contractor on inputs that reproduce its known performance cliffs.

Not part of scripts/run-tests.sh. Builds bin/contractor-bench, writes each
case's input (scripts/bench/gen.py) to a temporary directory, runs it at every
thread count, and prints one Markdown table row per run: wall time, CPU time,
peak RSS, and total bytes allocated (from the binary's -memprofile). The
output of the case chain is checked exactly against its closed form; a wrong
output fails the row and makes the script exit 1.

--compare REF runs every input through the binary and through the reference
binary REF at each thread count (default 1,8) and reports whether their stdout
is byte-identical, which certifies an output-preserving change on inputs too
large to be fixtures. Exits 1 if any output differs.

--args FLAGS passes extra flags, separated by spaces, to the binary under
test, never to REF.

Usage:
  scripts/bench/run.py                             all cases, default sizes
  scripts/bench/run.py times-scalar times-scalar-nf
  scripts/bench/run.py merge-one-key=4000          explicit size
  scripts/bench/run.py --scale 2                   every case at twice its default size
  scripts/bench/run.py --input workload.json       a user-supplied input file
  scripts/bench/run.py --threads 1,2,8 --repeat 3 --bin path/to/contractor
  scripts/bench/run.py --compare path/to/reference --input workload.json
  scripts/bench/run.py --compare path/to/reference --args=-merge-memory=65536

`scripts/bench/gen.py` with no arguments lists the cases and what SIZE means.
Wall times are noisy on a busy machine: compare rows from one session, use
--repeat (the fastest run is kept), and read the members of a pair (e.g.
root-bare and root-prefactor) side by side.
"""

import argparse
import functools
import json
import os
import re
import resource
import subprocess
import sys
import tempfile
import time
from fractions import Fraction
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
ROOT = SCRIPT_DIR.parents[1]
GEN = SCRIPT_DIR / "gen.py"
BENCH_BIN = ROOT / "bin" / "contractor-bench"
UNITS = {"B": 1 / 2**20, "kB": 1 / 2**10, "MB": 1, "GB": 2**10, "TB": 2**20}

sys.dont_write_bytecode = True
sys.path.insert(0, str(SCRIPT_DIR))
from gen import CASES, mom, pair, scaled_size  # noqa: E402


class Poly(dict):
    """A polynomial in D, p.p, p.q and q.q, as {exponents: coefficient}."""

    def __add__(self, other):
        out = dict(self)
        for e, c in other.items():
            out[e] = out.get(e, 0) + c
        return Poly((e, c) for e, c in out.items() if c)

    def __mul__(self, other):
        out = Poly()
        for ea, ca in self.items():
            for eb, cb in other.items():
                e = tuple(i + j for i, j in zip(ea, eb))
                out[e] = out.get(e, 0) + ca * cb
        return Poly((e, c) for e, c in out.items() if c)


def var(c, i=None):
    """c times variable i (D, p.p, p.q, q.q for 0 to 3), or the constant c."""
    return Poly({tuple(int(k == i) for k in range(4)): Fraction(c)} if c else {})


SCALAR_PRODUCTS = {json.dumps(pair(mom(a), mom(b))): 1 + (a == "q") + (b == "q") for a in "pq" for b in "pq"}


def polynomial(e):
    if type(e) is int:
        return var(e)
    if e == "D":
        return var(1, 0)
    if json.dumps(e) in SCALAR_PRODUCTS:
        return var(1, SCALAR_PRODUCTS[json.dumps(e)])
    if isinstance(e, list) and e[0] == "Rational":
        return var(Fraction(e[1], e[2]))
    if isinstance(e, list) and e[0] == "Plus":
        return sum(map(polynomial, e[1:]), Poly())
    if isinstance(e, list) and e[0] == "Times":
        return functools.reduce(Poly.__mul__, map(polynomial, e[1:]), var(1))
    raise ValueError(f"not a polynomial in D and scalar products: {json.dumps(e)[:60]}")


def chain_expected(n):
    """D - 2 + tr(M^n) with M = [[1+x, y], [2y, 1+2z]], x = p.p, y = p.q, z = q.q,
    from t_n = tr(M) t_(n-1) - det(M) t_(n-2), t_0 = 2, t_1 = tr(M)."""
    x, y, z = var(1, 1), var(1, 2), var(1, 3)
    tr = var(2) + x + var(2) * z
    det = (var(1) + x) * (var(1) + var(2) * z) + var(-2) * y * y
    t0, t1 = var(2), tr
    for _ in range(n - 1):
        t0, t1 = t1, tr * t1 + var(-1) * det * t0
    return var(1, 0) + var(-2) + t1


def check_chain(n, output):
    """None if output is the closed form of the chain of n links, else what is wrong."""
    try:
        got = polynomial(json.loads(output))
    except ValueError as err:
        return str(err)
    return None if got == chain_expected(n) else "differs from the closed form"


CHECKS = {"chain": check_chain}


def vm_hwm_kb(pid):
    try:
        with open(f"/proc/{pid}/status") as f:
            for line in f:
                if line.startswith("VmHWM:"):
                    return int(line.split()[1])
    except OSError:
        pass
    return 0


def total_alloc_mb(prof):
    out = subprocess.run(["go", "tool", "pprof", "-sample_index=alloc_space", "-top", "-nodecount=1", prof],
                         capture_output=True, text=True).stdout
    m = re.search(r"of ([\d.]+)([kMGT]?B) total", out)
    return float(m.group(1)) * UNITS[m.group(2)] if m else float("nan")


def measure(binary, flags, threads, path, tmp, timeout, check=None):
    """One run. Peak RSS combines /proc polling with wait4: the kernel charges a
    child the parent's RSS at exec, so wait4 is trusted only above that floor."""
    prof = os.path.join(tmp, "mem.prof")
    out = os.path.join(tmp, "stdout") if check else os.devnull
    floor = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    with open(path, "rb") as stdin, open(out, "wb") as stdout, \
            open(os.path.join(tmp, "stderr"), "w+b") as stderr:
        start = time.perf_counter()
        proc = subprocess.Popen([str(binary), "-threads", str(threads), "-memprofile", prof, *flags],
                                stdin=stdin, stdout=stdout, stderr=stderr)
        hwm = 0
        while True:
            hwm = max(hwm, vm_hwm_kb(proc.pid))
            pid, status, usage = os.wait4(proc.pid, os.WNOHANG)
            if pid:
                break
            if time.perf_counter() - start > timeout:
                proc.kill()
                proc.wait()
                return "timeout"
            time.sleep(0.005)
        wall = time.perf_counter() - start
        proc.returncode = os.waitstatus_to_exitcode(status)
        if proc.returncode != 0:
            stderr.seek(0)
            print(f"{path.name}: exit {proc.returncode}: {stderr.read().decode(errors='replace').strip()}",
                  file=sys.stderr)
            return f"exit {proc.returncode}"
    if check:
        with open(out, encoding="utf-8") as f:
            wrong = check(f.read())
        if wrong:
            return f"wrong output: {wrong}"
    rss_kb = max(hwm, usage.ru_maxrss if usage.ru_maxrss > floor else 0)
    return wall, usage.ru_utime + usage.ru_stime, rss_kb / 1024, total_alloc_mb(prof)


def compare(binary, flags, ref, threads, path, tmp):
    """Wall times of ref and binary on one input, the size of binary's stdout,
    and whether the two runs' exit codes and stdout agree. Only binary gets
    flags."""
    runs = []
    for b, extra in ((ref, []), (binary, flags)):
        out = os.path.join(tmp, f"{len(runs)}.out")
        with open(path, "rb") as stdin, open(out, "wb") as stdout:
            start = time.perf_counter()
            code = subprocess.run([str(b), "-threads", str(threads), *extra], stdin=stdin, stdout=stdout).returncode
            runs.append((out, code, time.perf_counter() - start))
    (ref_out, ref_code, ref_wall), (out, code, wall) = runs
    if ref_code != code:
        verdict = f"exit {ref_code} vs {code}"
    elif subprocess.run(["cmp", "-s", ref_out, out]).returncode == 0:
        verdict = "identical" if code == 0 else f"identical, exit {code}"
    else:
        verdict = "DIFFERS"
    return ref_wall, wall, os.path.getsize(out), verdict


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("cases", nargs="*", metavar="CASE[=SIZE]")
    ap.add_argument("--input", action="append", default=[], metavar="FILE", help="also run this input file; repeatable")
    ap.add_argument("--threads", help="comma-separated -threads values (default 1,4; 1,8 with --compare)")
    ap.add_argument("--scale", type=float, default=1.0, help="multiply default sizes (see gen.py's scaled_size)")
    ap.add_argument("--repeat", type=int, default=1, help="runs per row; the fastest is kept")
    ap.add_argument("--bin", type=Path, help="benchmark this binary instead of building one")
    ap.add_argument("--timeout", type=float, default=600, help="seconds per run (default 600)")
    ap.add_argument("--compare", type=Path, metavar="REF", help="check that stdout is byte-identical to REF's")
    ap.add_argument("--args", default="", metavar="FLAGS",
                    help="extra flags for the binary under test, not REF, separated by spaces")
    args = ap.parse_args()
    flags = args.args.split()
    sys.stdout.reconfigure(line_buffering=True)

    selected = []
    for spec in args.cases or ([] if args.input else list(CASES)):
        name, _, size = spec.partition("=")
        if name not in CASES:
            ap.error(f"unknown case {name!r}; known: {', '.join(CASES)}")
        selected.append((name, int(size) if size else scaled_size(name, args.scale)))
    threads = [int(t) for t in (args.threads or ("1,8" if args.compare else "1,4")).split(",")]

    binary = args.bin
    if binary is None:
        subprocess.run(["go", "build", "-o", str(BENCH_BIN), "./cmd/contractor"], cwd=ROOT, check=True)
        binary = BENCH_BIN

    load = f"{os.cpu_count()} CPUs, load average {' '.join(f'{x:.2f}' for x in os.getloadavg())}"
    under_test = " ".join([str(binary), *flags])
    if args.compare:
        print(f"{under_test} against {args.compare}, {load}")
        print()
        print("| case | size | input MB | threads | ref wall s | wall s | output MB | stdout |")
        print("|---|---:|---:|---:|---:|---:|---:|---|")
    else:
        print(f"{under_test}, {load}")
        print()
        print("| case | size | input MB | threads | wall s | CPU s | peak RSS MB | alloc MB |")
        print("|---|---:|---:|---:|---:|---:|---:|---:|")
    failed = False
    with tempfile.TemporaryDirectory(prefix="contractor-bench-") as tmp:
        def report(name, size, path, check=None):
            nonlocal failed
            row = f"| {name} | {size} | {path.stat().st_size / 2**20:.2f} |"
            for t in threads:
                if args.compare:
                    ref_wall, wall, out, verdict = compare(binary, flags, args.compare, t, path, tmp)
                    failed |= not verdict.startswith("identical")
                    print(f"{row} {t} | {ref_wall:.2f} | {wall:.2f} | {out / 2**20:.2f} | {verdict} |")
                    continue
                results = [measure(binary, flags, t, path, tmp, args.timeout, check) for _ in range(args.repeat)]
                failed |= any(str(r).startswith("wrong output") for r in results)
                ok = [r for r in results if isinstance(r, tuple)]
                if ok:
                    wall, cpu, rss, alloc = min(ok)
                    cells = f"{wall:.2f} | {cpu:.2f} | {rss:.0f} | {alloc:.0f}"
                else:
                    cells = f"{results[0]} | - | - | -"
                print(f"{row} {t} | {cells} |")

        for name, size in selected:
            path = Path(tmp) / f"{name}.json"
            with open(path, "wb") as out:
                subprocess.run([sys.executable, str(GEN), name, str(size)], stdout=out, check=True)
            report(name, size, path, functools.partial(CHECKS[name], size) if name in CHECKS else None)
        for f in args.input:
            report(Path(f).name, "-", Path(f))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
