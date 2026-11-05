# Go SDK backports

The SDK baseline is Persona `ed75671c`. This is not a full sync to a newer SDK.
Only the open-object `Contributes` JSON methods were regenerated with the generator
from Persona `08a782c2eeed292a051623e7031f4b0c45c71210` (upstream issue #2467 / PR #2577).
The schemas, runtime handlers, declared fields and other SDK files stay at the baseline.
The two upstream regression tests and their synthetic fixture are copied unchanged.
The baseline SDK test fixtures are also copied byte-for-byte, making the complete
nested SDK test suite self-contained; none are from a newer protocol version.

The generated change is 48 added lines: preserve opaque additional JSON properties,
match known keys by exact spelling, and prevent extras from shadowing declared fields.
No TypeScript SDK, new extension capabilities, manifest version, market index,
signature or release is part of this backport.

## Reproduce and verify

Use a Persona checkout containing the pinned generator file. Its SHA256 and every
schema input are checked before generating, so an unrelated newer checkout is not
silently accepted. The command recomputes the full Go output from the existing schemas;
it compares bytes rather than splicing generated declarations. It also verifies that
the test and fixture bytes are the exact pinned upstream copies. Paths are arguments,
not personal machine paths embedded in this repository.

```sh
python3 -I - <persona-checkout> <extension-repository> <<'PY'
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys

source, repo = (Path(p).resolve() for p in sys.argv[1:])
def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

generator = source / "scripts/gen/extension_schema.py"
assert digest(generator) == "fa7c9136936f6572b895beb4b0869b338c40c3504e321ebd8d38ece087f970ab"
expected_schemas = {'manifest.schema.json': '5e4e99e6ef9af380127c1bafa9a91c253447df7df55d0339bde51b5fdd9253c3', 'protocol.schema.json': '18eca32f877881d82bab882b25ce0cfe9977dcdd963a86e3df3584ed88e68963', 'view.schema.json': 'e42d809add2ae322494209c67469d6603cbea8aec4fed802e44635ac2fd8fc49'}
for name, expected in expected_schemas.items():
    assert digest(repo / "sdk/go/wire/schema" / name) == expected
spec = importlib.util.spec_from_file_location("extension_codegen", generator)
gen = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = gen
spec.loader.exec_module(gen)
gen.SCHEMA_DIR = repo / "sdk/go/wire/schema"
output = gen.gofmt(gen.gen_go(gen.load())).rstrip("\n") + "\n"
assert (repo / "sdk/go/wire/wire_gen.go").read_text() == output
expected_copies = {'sdk/go/ext/custom_contributes_test.go': '52d30794d903b58a1a010f6998de3837152b3cfb71552f6c201a9c153b9f5273', 'sdk/go/wire/custom_contributes_test.go': '6ad51ee3cb4d52cc4fb75d7bfbd2199cc07195c698cd6b199ea20c8fe84edd4b', 'schema/extension/examples/Manifest.custom-contributions.json': '61ed686e7665b1c2c5708e56925efde8d03ecfff646f8c24bd5964e74979af9c'}
for name, expected in expected_copies.items():
    assert digest(source / name) == expected
    assert digest(repo / name) == expected
provenance_path = repo / "sdk/BASELINE_FIXTURES.json"
assert digest(provenance_path) == "9b40e6b7f836d347645722db827101ded44b3ae92084c467a74d7646bbf9c776"
provenance = json.loads(provenance_path.read_text())
assert provenance["commit"] == "ed75671c7a2419eb815a0e0d6bcb4bd73bf2c46b"
for name, expected in provenance["files"].items():
    original = subprocess.run(["git", "-C", str(source), "show", provenance["commit"] + ":" + name],
                              capture_output=True, check=True).stdout
    assert hashlib.sha256(original).hexdigest() == expected
    assert digest(repo / name) == expected
print("custom-contributes backport: reproducible output and exact upstream test inputs")
PY

go -C <extension-repository>/sdk/go test -count=1 -timeout=90s ./...
go -C <extension-repository>/sdk/go vet ./...
```

The SDK tests exercise typed JSON round trips and the actual JSON-lines `manifest` /
`initialize` handlers, including custom points, nested JSON, case-variant known keys,
reserved-field collision protection and values reused for subsequent decodes.
The repository's `scripts/ci.sh` runs these tests as well as its existing checks.

The SDK previously referenced example directories that were not vendored here, so
its bare full suite failed on missing fixtures. This backport adds the original
baseline inputs actually read by the tests, retaining all cases rather than selecting
a subset to satisfy count assertions. `sdk/BASELINE_FIXTURES.json` records their full
source commit and per-file SHA256. No showcase snapshots or newer schema examples
are imported. The CI now runs `sdk/go`'s complete `./...` suite in a clean checkout.
