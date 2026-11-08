#!/usr/bin/env python3
"""Create a deterministic source archive using the portable Python standard library."""
import gzip
import pathlib
import sys
import tarfile


def main():
    stage, archive = (pathlib.Path(p).resolve() for p in sys.argv[1:])
    with archive.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed, tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as packed:
        for path in [stage, *sorted(stage.rglob("*"))]:
            if path.is_symlink() or not (path.is_dir() or path.is_file()):
                raise ValueError("Only contained regular source files and directories may be packed")
            info = packed.gettarinfo(str(path), arcname=str(path.relative_to(stage.parent)))
            info.uid = info.gid = info.mtime = 0
            info.uname = info.gname = ""
            info.mode = 0o755 if path.is_dir() else 0o644
            info.pax_headers = {}
            if path.is_file():
                with path.open("rb") as source:
                    packed.addfile(info, source)
            else:
                packed.addfile(info)
    print("PASS: portable deterministic source archive")


if __name__ == "__main__":
    main()
