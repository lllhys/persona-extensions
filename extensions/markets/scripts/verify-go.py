#!/usr/bin/env python3
"""Run original Go race tests and overlay oracles without modifying frozen sources."""
import argparse
import hashlib
import json
import os
import pathlib
import subprocess

ROOT = pathlib.Path(__file__).resolve().parent.parent

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--go", type=pathlib.Path, required=True)
    args = parser.parse_args()
    go = args.go.resolve()
    version = subprocess.check_output([str(go), "version"], text=True, timeout=10).strip()
    if version != "go version go1.27.1 linux/amd64":
        raise RuntimeError("FAIL original declared Go toolchain unavailable")
    files = [p for p in ROOT.iterdir() if p.suffix == ".go" or p.name in ["go.mod", "go.sum"]]
    before = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    build = ROOT / "build"
    original = ROOT / "markets_test.go"
    overlay_test = build / "round3-overlay.txt"
    text = original.read_text().replace('import (', 'import (\n "math"\n "strconv"', 1)
    text += "\n" + (ROOT / "tests/go-boundaries.txt").read_text() + "\n" + (ROOT / "tests/round3-go.txt").read_text()
    overlay_test.write_text(text)
    overlay = build / "round3-overlay.json"
    overlay.write_text(json.dumps({"Replace": {str(original): str(overlay_test)}}))
    env = dict(os.environ, GOROOT=str(go.parent.parent), GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="4")
    results = []
    for name, options in [("original", []), ("overlay", ["-overlay=" + str(overlay)])]:
        run = subprocess.run([str(go), "test", "-race", "-count=1", "-timeout=90s", "-json", *options, "."], cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=150)
        (build / ("round3-go-" + name + ".log")).write_bytes(run.stdout)
        events = []
        for line in run.stdout.splitlines():
            try:
                events.append(json.loads(line))
            except ValueError:
                pass
        results.append({"suite": name, "exit": run.returncode, "passed_tests": sum(e.get("Action") == "pass" and "Test" in e for e in events), "failed_tests": sum(e.get("Action") == "fail" and "Test" in e for e in events)})
    after = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    if before != after:
        raise RuntimeError("FAIL original Go/mod hashes changed")
    print(json.dumps({"toolchain": version, "results": results, "original_go_mod_files_unchanged": len(files)}, sort_keys=True))
    if any(r["exit"] for r in results):
        raise RuntimeError("FAIL original Go race/oracle tests")
    print("VERDICT original Go race and overlay: PASS")

if __name__ == "__main__":
    try:
        main()
    except subprocess.TimeoutExpired:
        print("VERDICT original Go race and overlay: TIMEOUT")
        raise SystemExit(1)
    except Exception as error:
        print(str(error))
        print("VERDICT original Go race and overlay: FAIL")
        raise SystemExit(1)
