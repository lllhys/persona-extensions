#!/usr/bin/env python3
"""Package a controlled staging directory with portable, deterministic stdlib tar/gzip."""
import gzip
from pathlib import Path
import sys
import tarfile


def main():
    stage, archive = map(Path, sys.argv[1:])
    paths = [stage, *sorted(stage.rglob("*"))]
    if any(path.is_symlink() or not (path.is_file() or path.is_dir()) for path in paths):
        raise ValueError("source archive requires regular files and directories")
    with archive.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as tar:
                for path in paths:
                    info = tar.gettarinfo(str(path), str(path.relative_to(stage.parent)))
                    info.uid = info.gid = info.mtime = 0
                    info.uname = info.gname = ""
                    info.mode = 0o755 if path.is_dir() else 0o644
                    info.pax_headers = {}
                    if path.is_file():
                        with path.open("rb") as content:
                            tar.addfile(info, content)
                    else:
                        tar.addfile(info)
    print("source archive: PASS (Python stdlib tar/gzip; no GNU tar flags)", flush=True)


if __name__ == "__main__":
    try:
        main()
    except Exception:
        print("source archive: FAIL (invalid staging tree or packaging failure)", flush=True)
        raise SystemExit(1)
