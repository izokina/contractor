#!/usr/bin/env python3
"""Run every entry of scripts/sample-fixtures.json through the contractor and
verify each run.

Fixture entries:
  name       unique identifier used in docs/samples.md
  input      ExpressionJSON tree, sent as compact JSON
  input_raw  alternatively, text piped to stdin verbatim (UTF-8)
  outputs    expected results; a run must match one of them
  error      alternatively, a non-empty part of the message the input must
             be rejected with
  permute    optional, `input` only, on a root `Plus` of two or more
             summands: also run the input with those summands reversed and
             in a seeded shuffle

Each input variant runs RUNS_PER_THREADS times at each of THREADS, and, given
a race binary, once at each of RACE_THREADS through it, every run with the
flags of --args, if any. A run passes when:
  - it finishes within TIMEOUT seconds (RACE_TIMEOUT for the race build);
  - stderr is ASCII and reports no data race;
  - for `outputs`: exit status 0, empty stderr, and stdout is one JSON value
    matching one of `outputs`, with every non-ASCII character written as
    literal UTF-8 rather than as a \\u escape;
  - for `error`: exit status 1 (a crash exits with 2), empty stdout, and the
    `error` text in stderr.

Comparison is exact on JSON types, values and order at every level;
whitespace is ignored. Numbers are kept as their source text, so `5` differs
from `5.0` and from the string `"5"`.

A failing entry gets a verdict per input variant: deterministic (every run
failed), flaky (the same variant both passed and failed) or race (a run
through the race build reported a data race; such runs count toward neither
of the other two).

Usage:
  scripts/check-fixtures.py [--race-bin RACE_BINARY] [--args=FLAGS] [BINARY]

BINARY defaults to bin/contractor; without --race-bin the race pass is skipped.
"""

import argparse
import json
import os
import random
import re
import subprocess
import sys
from collections import namedtuple
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = SCRIPT_DIR.parent
FIXTURE = SCRIPT_DIR / "sample-fixtures.json"
DEFAULT_BIN = PROJECT_ROOT / "bin" / "contractor"
THREADS = (1, 8)
RUNS_PER_THREADS = 10
RACE_THREADS = (1, 8)
TIMEOUT = 2
# The race build runs several times slower than the normal one.
RACE_TIMEOUT = 10 * TIMEOUT
# Without this, a race-enabled binary sleeps 1 s before exiting.
RACE_ENV = {**os.environ, "GORACE": "atexit_sleep_ms=0"}
RACE = "data race"
TIMED_OUT = "timeout"
# A JSON escape; group 1 holds the code unit of a \uXXXX escape.
ESCAPE = re.compile(rb"\\(?:u([0-9a-fA-F]{4})|.)", re.DOTALL)

Setup = namedtuple("Setup", "name binary threads env runs timeout args")


class JSONNumber:
    """A JSON number kept as its source text. It equals only another
    JSONNumber with the same text, never a string."""

    __slots__ = ("text",)

    def __init__(self, text):
        self.text = text

    def __eq__(self, other):
        return isinstance(other, JSONNumber) and self.text == other.text


def reject_constant(name):
    raise ValueError(f"{name} is not JSON")


def loads(text):
    return json.loads(text, parse_int=JSONNumber, parse_float=JSONNumber, parse_constant=reject_constant)


def dumps(obj):
    if isinstance(obj, JSONNumber):
        return obj.text
    if isinstance(obj, list):
        return "[" + ",".join(dumps(x) for x in obj) + "]"
    if isinstance(obj, dict):
        return (
            "{"
            + ",".join(
                f"{json.dumps(k, ensure_ascii=False)}:{dumps(v)}"
                for k, v in obj.items()
            )
            + "}"
        )
    return json.dumps(obj, ensure_ascii=False)


def render(obj):
    """dumps for a failure report, which must not fail on a deep value."""
    try:
        return dumps(obj)
    except RecursionError:
        return "(too deep to render)"


def headed(x, head):
    return isinstance(x, list) and len(x) > 0 and x[0] == head


FIELDS = {"name", "input", "input_raw", "outputs", "error", "permute"}


def check_fixture(entries):
    problems = [p for entry in entries for p in check_entry(entry)]
    names = [entry.get("name") for entry in entries]
    duplicates = {n for n in names if isinstance(n, str) and names.count(n) > 1}
    problems += [f"{name}: duplicate name" for name in sorted(duplicates)]
    return problems


def check_entry(entry):
    name = entry.get("name")
    problems = []
    if not isinstance(name, str):
        name = "<entry without a string name>"
        problems.append("needs a string name")
    if set(entry) - FIELDS:
        problems.append(f"unknown fields {sorted(set(entry) - FIELDS)}")
    if ("input" in entry) == ("input_raw" in entry):
        problems.append("needs exactly one of input, input_raw")
    elif "input_raw" in entry and not isinstance(entry["input_raw"], str):
        problems.append("input_raw must be a string")
    if ("outputs" in entry) == ("error" in entry):
        problems.append("needs exactly one of outputs, error")
    elif "outputs" in entry and not (isinstance(entry["outputs"], list) and entry["outputs"]):
        problems.append("outputs must be a non-empty list")
    elif "error" in entry and not (isinstance(entry["error"], str) and entry["error"]):
        problems.append("error must be a non-empty string")
    if "permute" in entry and not (
        entry["permute"] is True
        and headed(entry.get("input"), "Plus")
        and len(entry["input"]) > 2
    ):
        problems.append("permute must be true, on an input whose root is a Plus of two or more summands")
    return [f"{name}: {p}" for p in problems]


def variants(entry):
    """The stdin texts to run, with a label each."""
    if "input_raw" in entry:
        return [("raw", entry["input_raw"])]
    root = entry["input"]
    found = [("as given", root)]
    if entry.get("permute"):
        summands = root[1:]
        shuffled = list(summands)
        rng = random.Random(entry["name"])
        for _ in range(10):
            rng.shuffle(shuffled)
            if shuffled not in (summands, summands[::-1]):
                break
        for label, order in (("reversed", summands[::-1]), ("shuffled", shuffled)):
            candidate = [root[0], *order]
            if all(candidate != seen for _, seen in found):
                found.append((label, candidate))
    return [(label, dumps(tree)) for label, tree in found]


def escaped_non_ascii(out):
    return [
        m.group(0).decode()
        for m in ESCAPE.finditer(out)
        if m.group(1) and int(m.group(1), 16) >= 0x80
    ]


def run_once(setup, stdin, entry):
    """Run the contractor once; return None on a pass, else (reason, detail)."""
    try:
        proc = subprocess.run(
            [str(setup.binary), "-threads", str(setup.threads), *setup.args],
            input=stdin.encode(),
            capture_output=True,
            timeout=setup.timeout,
            env=setup.env,
        )
    except subprocess.TimeoutExpired:
        return (TIMED_OUT, f"no exit within {setup.timeout} s")
    out, err = proc.stdout, proc.stderr
    err_text = err.decode(errors="replace").strip()
    if b"WARNING: DATA RACE" in err:
        return (RACE, err_text)
    if not err.isascii():
        return ("stderr not ASCII", err_text)
    if "error" in entry:
        if proc.returncode != 1:
            return (f"exit status {proc.returncode}, expected 1", err_text or out.decode(errors="replace"))
        if out:
            return ("stdout not empty on rejection", out.decode(errors="replace"))
        if entry["error"] not in err_text:
            return ("wrong error message", err_text)
        return None
    if proc.returncode != 0:
        return (f"exit status {proc.returncode}", err_text)
    if err:
        return ("stderr not empty", err_text)
    try:
        actual = loads(out.decode())
    except ValueError as e:
        return ("stdout is not one JSON value", f"{e}: {out.decode(errors='replace')}")
    except RecursionError as e:
        return ("stdout nested too deeply to parse", str(e))
    if escaped := escaped_non_ascii(out):
        return ("non-ASCII character written as an escape on stdout", " ".join(escaped))
    try:
        matched = actual in entry["outputs"]
    except RecursionError as e:
        return ("stdout nested too deeply to compare", str(e))
    if not matched:
        return ("output mismatch", render(actual))
    return None


def run_variant(setups, stdin, entry):
    """Run one input variant through every setup. Returns its verdict, the
    pass counts per setup, the first failure that is not a race, and the
    first race report."""
    counts = {}
    passed = failed = 0
    first = race = None
    for setup in setups:
        ok = ran = 0
        for _ in range(setup.runs):
            bad = run_once(setup, stdin, entry)
            ran += 1
            if bad is None:
                ok += 1
            elif bad[0] == RACE:
                race = race or (setup.name, bad[1])
            else:
                failed += 1
                first = first or (setup.name, bad)
                if bad[0] == TIMED_OUT:
                    break
        counts[setup.name] = (ok, ran)
        passed += ok
    if failed == 0:
        verdict = "passed"
    elif passed == 0:
        verdict = "deterministic"
    else:
        verdict = "flaky"
    return verdict, counts, first, race


def main():
    ap = argparse.ArgumentParser(description="Check the contractor against scripts/sample-fixtures.json.")
    ap.add_argument("binary", nargs="?", default=DEFAULT_BIN, type=Path, help="contractor binary (default: bin/contractor)")
    ap.add_argument("--race-bin", type=Path, help="contractor built with -race; without it the race pass is skipped")
    ap.add_argument("--args", default="", help="extra flags for every run, separated by spaces, such as --args=-merge-memory=1")
    args = ap.parse_args()
    extra = args.args.split()

    setups = [Setup(f"-threads {t}", args.binary.resolve(), t, None, RUNS_PER_THREADS, TIMEOUT, extra) for t in THREADS]
    if args.race_bin is not None:
        setups += [
            Setup(f"race -threads {t}", args.race_bin.resolve(), t, RACE_ENV, 1, RACE_TIMEOUT, extra)
            for t in RACE_THREADS
        ]
    for setup in setups:
        if not setup.binary.is_file():
            print(f"contractor binary not found: {setup.binary}", file=sys.stderr)
            print("hint: run ./scripts/run-tests.sh from the project root", file=sys.stderr)
            sys.exit(2)

    entries = loads(FIXTURE.read_text(encoding="utf-8"))
    problems = check_fixture(entries)
    if problems:
        print("malformed fixture entries:\n  " + "\n  ".join(problems), file=sys.stderr)
        sys.exit(2)

    failures = []
    for entry in entries:
        results = [(label, stdin, *run_variant(setups, stdin, entry)) for label, stdin in variants(entry)]
        kinds = {verdict for _, _, verdict, _, _, _ in results if verdict != "passed"}
        if any(race for *_, race in results):
            kinds.add("race")
        if kinds:
            failures.append((entry, sorted(kinds), results))

    total = len(entries)
    race_pass = (
        f"; race build: 1 run at each of -threads {', '.join(map(str, RACE_THREADS))}"
        if args.race_bin is not None
        else "; race pass skipped"
    )
    flags = f", every run with {' '.join(extra)}" if extra else ""
    print(
        f"{total - len(failures)}/{total} samples passed all runs "
        f"(per input variant: {RUNS_PER_THREADS} runs at each of -threads {', '.join(map(str, THREADS))}{race_pass}{flags})"
    )
    for entry, kinds, results in failures:
        if "error" in entry:
            expected = f"exit 1 with stderr containing {entry['error']!r}"
        else:
            expected = "one of:\n    " + "\n    ".join(render(o) for o in entry["outputs"])
        print(f"FAIL {entry['name']}: {', '.join(kinds)}\n  expected {expected}")
        for label, stdin, verdict, counts, first, race in results:
            tally = ", ".join(f"{name}: {ok}/{ran}" for name, (ok, ran) in counts.items())
            print(f"  variant {label}: {verdict} ({tally})")
            if first is None and race is None:
                continue
            print(f"    input:\n      {stdin}")
            if first is not None:
                setup, (reason, detail) = first
                print(f"    first failure ({setup}): {reason}\n      {detail}")
            if race is not None:
                setup, report = race
                print(f"    data race ({setup}):\n      " + report.replace("\n", "\n      "))
    if failures:
        print(
            "Verdicts per variant: deterministic = every run failed (a regression or a spec shift); "
            "flaky = the same variant both passed and failed; race = a run through the race build "
            "reported a data race (not counted as pass or failure in the other verdicts)."
        )
    sys.exit(0 if not failures else 1)


if __name__ == "__main__":
    main()
