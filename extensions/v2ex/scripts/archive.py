"""Write a deterministic source archive without GNU-specific tar flags."""
import gzip
from pathlib import Path
import sys
import tarfile

root, archive = map(Path, sys.argv[1:])
with archive.open("wb") as output, gzip.GzipFile(fileobj=output, mode="wb", filename="", mtime=0) as compressed:
    with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as tar:
        for path in sorted(root.rglob("*")):
            if path.is_symlink() or not (path.is_file() or path.is_dir()):
                raise ValueError("source archive requires regular files and directories")
            info = tar.gettarinfo(str(path), arcname=path.relative_to(root).as_posix())
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            info.mtime = 0
            info.mode = 0o755 if path.is_dir() else 0o644
            if path.is_file():
                with path.open("rb") as data:
                    tar.addfile(info, data)
            else:
                tar.addfile(info)
print("archive: PASS")
