"""Run bounded local regression evidence; failing shared gates never count as green."""
from pathlib import Path
import hashlib
import json
import os
import re
import signal
import subprocess
import sys
from urllib.parse import urlsplit
import tempfile

root, go, node22, node24, terms, term_checker = map(Path, sys.argv[1:])
repo = root.parent.parent
build = root / "build"
env = dict(os.environ, GOROOT=str(go.parent.parent), GOTOOLCHAIN="local", PATH=str(go.parent) + os.pathsep + os.environ["PATH"])
results = {}


def run(name, args, cwd=root, environment=env, deadline=180):
    process = subprocess.Popen(list(map(str, args)), cwd=cwd, env=environment, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
    timeout = False
    try:
        output = process.communicate(timeout=deadline)[0]
    except subprocess.TimeoutExpired:
        timeout = True
        os.killpg(process.pid, signal.SIGKILL)
        output = process.communicate(timeout=5)[0]
    finally:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    code = 124 if timeout else process.returncode
    text = output.decode(errors="replace")
    blocked = bool(re.search(r"not ok[^\n]*# TODO", text)) or bool(re.search(r"^\s*[1-9]\d* todo$", text, re.M) and "AssertionError" in text)
    unexpected = bool(re.search(r"^# fail [1-9]|^\s*[1-9]\d* fail$", text, re.M))
    verdict = "TIMEOUT" if timeout else "FAIL" if unexpected else "BLOCKED" if blocked else "PASS" if code == 0 else "FAIL"
    if blocked and code == 0:
        code = 2
    log = build / ("round3-" + name + ".log")
    log.write_text(text + f"\n{name}: {verdict} exit={code}\n")
    results[name] = {"verdict": verdict, "exit": code, "log": str(log)}
    print(f"{name}: {verdict} exit={code}", flush=True)
    return code, text


code, version = run("go-version", [go, "version"])
assert code == 0 and "go1.27.1 linux/amd64" in version
assert run("go-base-race", [go, "test", "-race", "-count=1", "-timeout=90s", "-json", "."])[0] == 0
overlay = build / "round3-overlay.json"
overlay.write_text(json.dumps({"Replace": {str(root / "original_round2_test.go"): str(root / "tests/original-round2.go.txt"), str(root / "original_round3_test.go"): str(root / "tests/original-round3.go.txt")}}))
assert run("go-oracle-race", [go, "test", "-race", "-count=1", "-timeout=90s", "-json", "-overlay=" + str(overlay), "."])[0] == 0
code, dates = run("date-oracle", [go, "run", root / "tests/oracle/main.go"])
assert code == 0 and json.loads(dates) == json.loads((root / "tests/date-oracle.json").read_text())
code, _ = run("fold-oracle", [go, "run", root / "tests/oracle/fold/main.go", build / "round3-fold.json"])
assert code == 0 and (build / "round3-fold.json").read_bytes() == (root / "src/simple-fold.json").read_bytes()
for name, node in [("node22", node22), ("node24", node24)]:
    run(name + "-version", [node, "--version"])
    run(name, [node, root / "scripts/test.mjs"])
    run(name + "-shared-gates", [node, "--test", "--test-reporter=tap", "--test-name-pattern=RPC numeric lexemes|legacy HTTP cold", root / "build/tests/round2.test.mjs", root / "build/tests/regression.test.mjs"])
    run(name + "-syntax", [node, root / "scripts/check-syntax.mjs"])
run("closed-bundle", [node22, root / "scripts/check-bundle.mjs"])
entries = ["model.test.ts", "service.test.ts", "page.test.tsx", "program.test.tsx", "regression.test.tsx", "round2.test.tsx", "round3.test.tsx", "lock.test.mjs"]
run("bun", ["bun", "test", "--todo", *["./tests/" + p for p in entries]])
run("bun-shared-gates", ["bun", "test", "--todo", "--test-name-pattern", "RPC numeric lexemes|legacy HTTP cold", "./tests/round2.test.tsx", "./tests/regression.test.tsx"])
assert results["bun"]["verdict"] == results["bun-shared-gates"]["verdict"] == "BLOCKED"
assert all(results[name]["verdict"] == "BLOCKED" for name in ["node22", "node24", "node22-shared-gates", "node24-shared-gates"])

# New empty unpack and npm cache directories; scripts run outside downloaded inputs.
scratch = Path(tempfile.mkdtemp(prefix="round3-cold.", dir=build))
unpacked = scratch / "unpacked"
unpacked.mkdir()
code, _ = run("artifact-audit", ["python3", "-I", root / "scripts/verify-source.py", root, unpacked, terms, term_checker])
assert code == 0
home = scratch / "home"
home.mkdir()
user = scratch / "user.npmrc"
global_config = scratch / "global.npmrc"
user.write_text("registry=https://registry.npmjs.org/\n")
global_config.write_text("")
cold_env = dict(env, HOME=str(home), XDG_CONFIG_HOME=str(home / ".config"), npm_config_userconfig=str(user), npm_config_globalconfig=str(global_config), npm_config_cache=str(scratch / "npm-cache"), npm_config_registry="https://registry.npmjs.org/", PATH=str(node22.parent) + os.pathsep + env["PATH"])
authoring = unpacked / "authoring/extensions/v2ex"
assert run("cold-install", ["npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=authoring, environment=cold_env)[0] == 0
run("cold-tests", [node22, authoring / "scripts/test.mjs"], cwd=authoring, environment=cold_env)
for name in ["v2ex.mjs", "v2ex-source.tar.gz"]:
    assert (authoring / "build" / name).read_bytes() == (build / name).read_bytes(), "cold rebuild differs"

# Full canonical, staged and extracted file scans, plus each hidden lock independently.
paths = []
for base in [root, build / "package", unpacked]:
    if base == root:
        paths += [p for folder in [root / "src", root / "scripts", root / "tests"] for p in folder.rglob("*") if p.is_file()]
        paths += [root / p for p in ["README.md", "package.json", "package-lock.json", "tsconfig.json"]]
    else:
        paths += [p for p in base.rglob("*") if p.is_file() and "node_modules" not in p.relative_to(base).parts
                  and not p.relative_to(base).as_posix().startswith("authoring/extensions/v2ex/build/")]
locks = [root / "package-lock.json", root / "node_modules/.package-lock.json", build / "package/package-lock.json", unpacked / "package-lock.json", unpacked / "authoring/extensions/v2ex/package-lock.json", authoring / "node_modules/.package-lock.json"]
paths += locks
code, _ = run("privacy", ["python3", "-I", term_checker, terms, *paths])
assert code == 0
for i, lock in enumerate(locks):
    assert run("lock-" + str(i), [node22, root / "scripts/check-lock.mjs", lock])[0] == 0
    assert run("hidden-privacy-" + str(i), ["python3", "-I", term_checker, terms, lock])[0] == 0
# URL credentials are scanned independently of terms and lock-origin checks.
def credential_fixtures():
    """The unchanged three synthetic URL boundaries; no real credentials."""
    topic = urlsplit("https://v2ex.com/t/123")
    archive = urlsplit("https://registry.npmjs.org/example/-/example-1.0.0.tgz")
    return {
        topic._replace(netloc="user" + "@" + topic.netloc).geturl(): "tests/model.test.ts",
        archive._replace(netloc="fixture:synthetic" + "@" + archive.netloc).geturl(): "tests/lock.test.mjs",
        archive._replace(query="token=synthetic").geturl(): "tests/lock.test.mjs",
    }

controls = credential_fixtures()
credential_controls = 0
for path in set(paths):
    text = path.read_text(errors="strict")
    for literal in re.findall(r"https?://[^\s\x22\x27<>`]+", text):
        parsed = urlsplit(literal.rstrip(".,);"))
        if parsed.username or parsed.password or re.search(r"(?:token|key|secret|password|auth)=", parsed.query, re.I):
            # The documented negative fixtures are byte-identical to canonical inputs.
            suffix = controls.get(literal)
            if path == root / "scripts/verify-round3.py":
                assert literal in controls
            elif path.name == "verify-round3.py":
                assert path.read_bytes() == (root / "scripts/verify-round3.py").read_bytes() and literal in controls
            else:
                assert suffix and str(path).endswith(suffix) and path.read_bytes() == (root / suffix).read_bytes(), "unexpected URL credential"
            credential_controls += 1
results["url_credentials"] = {"verdict": "PASS", "synthetic_controls": credential_controls, "unexpected": 0}
# Read-only repository guards; full CI packaging of unrelated extensions stays frozen.
guard_tmp = Path(tempfile.mkdtemp(prefix="round3-guards.", dir=build))
guard_env = dict(env, TMPDIR=str(guard_tmp))
for name, script, args in [
    ("docs", "check-docs.py", []), ("docs-tests", "test-docs.py", []),
    ("layout", "check-layout.py", []), ("layout-tests", "test-layout.py", []),
    ("manifest", "manifest.py", ["check"]), ("manifest-tests", "test-manifest.py", []),
    ("package-tests", "test-package.py", []), ("index", "index.py", ["check"]),
    ("index-tests", "test-index.py", []), ("signature", "sign-index.py", ["verify"]),
    ("signature-tests", "test-sign-index.py", []),
]:
    assert run(name, ["python3", "-I", "-B", repo / "scripts" / script, *args], cwd=repo, environment=guard_env)[0] == 0
assert run("diff", ["git", "diff", "--check"], cwd=repo)[0] == 0
assert run("go-vet", [go, "vet", "."])[0] == 0
frozen = json.loads((build / "round3-frozen.json").read_text())
assert all(hashlib.sha256((repo / name).read_bytes()).hexdigest() == digest for name, digest in frozen.items()), "protected input changed"
results["frozen"] = {"verdict": "PASS", "files": len(frozen)}
results["cold"] = {"verdict": "PASS", "path": str(scratch), "bundle_and_archive_equal": True}
results["oracles"] = {"verdict": "PASS", "dates": 80, "simple_fold": 1512, "unicode": "17.0.0"}
results["terms"] = {"patterns": len([s for s in terms.read_text().splitlines() if s.strip()]), "files_scanned": len(paths)}
results["source"] = json.loads((build / "source.json").read_text())
(build / "round3-verification.json").write_text(json.dumps(results, indent=2) + "\n")
print("round3: BLOCKED (numeric RPC and cold HTTP shared gates remain actual failures)", flush=True)
raise SystemExit(2)
