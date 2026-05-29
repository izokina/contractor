#!/usr/bin/env python3
"""Pipe each input from scripts/sample-fixtures.json into ./bin/contractor 20
times and verify every run's output matches one of the entry's expected
`outputs`. A sample is reported as a failure if any of its 20 runs diverges;
the report shows one example bad output per failing sample plus a runs-passed
stat.

Comparison is byte-correct on strings and numbers; whitespace/indentation is
ignored. The outermost `Plus` is compared as a multiset of addends (Go map
iteration order across pair-keys is non-deterministic per docs/samples.md).

Number byte-correctness is achieved with a `JSONNumber` type that subclasses
str: numbers parsed from JSON keep their source text (so `5` ≠ `5.0`,
`0.5` ≠ `0.50`), and a custom emitter writes them back unquoted so the
round-trip into the contractor is transparent.

Usage:
  scripts/check-fixtures.py
  scripts/check-fixtures.py path/to/binary
  CONTRACTOR_BIN=path scripts/check-fixtures.py
"""

import json
import os
import subprocess
import sys
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = SCRIPT_DIR.parent
FIXTURE = SCRIPT_DIR / "sample-fixtures.json"
DEFAULT_BIN = PROJECT_ROOT / "bin" / "contractor"
RUNS_PER_SAMPLE = 20


class JSONNumber(str):
    """A JSON number kept as its source-text form. Equality is str equality
    (byte-correct). `to_json` returns the raw text so the round-trip through
    `dumps` re-emits the same bytes unquoted."""

    def to_json(self):
        return str.__str__(self)


def loads(text):
    return json.loads(text, parse_int=JSONNumber, parse_float=JSONNumber)


def dumps(obj):
    if isinstance(obj, JSONNumber):
        return obj.to_json()
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


def equal_modulo_top_plus(expected, actual):
    if (
        isinstance(expected, list)
        and isinstance(actual, list)
        and expected
        and actual
        and expected[0] == "Plus"
        and actual[0] == "Plus"
    ):
        if len(expected) != len(actual):
            return False
        return sorted(expected[1:], key=dumps) == sorted(actual[1:], key=dumps)
    return expected == actual


def run_contractor(binary, input_obj):
    proc = subprocess.run(
        [str(binary)],
        input=dumps(input_obj).encode(),
        capture_output=True,
        timeout=30,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"exit {proc.returncode}: "
            f"{proc.stderr.decode(errors='replace').strip()}"
        )
    return proc.stdout.decode()


def main():
    if len(sys.argv) > 2:
        print("usage: check-fixtures.py [contractor-binary]", file=sys.stderr)
        sys.exit(2)
    binary = Path(
        sys.argv[1]
        if len(sys.argv) == 2
        else os.environ.get("CONTRACTOR_BIN", DEFAULT_BIN)
    )
    if not binary.exists():
        print(f"contractor binary not found: {binary}", file=sys.stderr)
        print("hint: run `make build` from the project root", file=sys.stderr)
        sys.exit(2)

    entries = loads(FIXTURE.read_text())
    failures = []
    for entry in entries:
        passes = 0
        example_bad = None
        example_error = None
        for _ in range(RUNS_PER_SAMPLE):
            try:
                actual = loads(run_contractor(binary, entry["input"]))
            except Exception as e:
                if example_bad is None and example_error is None:
                    example_error = str(e)
                continue
            if any(equal_modulo_top_plus(exp, actual) for exp in entry["outputs"]):
                passes += 1
            elif example_bad is None and example_error is None:
                example_bad = actual
        if passes < RUNS_PER_SAMPLE:
            failures.append((entry, passes, example_bad, example_error))

    total = len(entries)
    print(f"{total - len(failures)}/{total} samples passed all {RUNS_PER_SAMPLE} runs")
    for entry, passes, actual, err in failures:
        name = entry["name"]
        header = f"FAIL {name} ({passes}/{RUNS_PER_SAMPLE} runs passed):"
        if err is not None:
            print(f"{header} contractor error: {err}\n  input:\n    {dumps(entry['input'])}")
        else:
            print(
                f"{header}\n  input:\n    {dumps(entry['input'])}"
                + "\n  expected one of:\n    "
                + "\n    ".join(dumps(o) for o in entry["outputs"])
                + f"\n  example actual:\n    {dumps(actual)}"
            )
    sys.exit(0 if not failures else 1)


if __name__ == "__main__":
    main()
