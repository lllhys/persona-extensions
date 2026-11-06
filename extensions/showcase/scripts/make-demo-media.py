#!/usr/bin/env python3
"""Reproduce the public synthetic showcase PNGs without external dependencies."""
import pathlib
import struct
import sys
import zlib


def chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))


def image(width, height):
    pixels = bytearray()
    for y in range(height):
        pixels.append(0)
        for x in range(width):
            tile = (x * 4 // width + y * 4 // height) % 2
            pixels.extend((92, 138, 182) if tile else (222, 234, 245))
    header = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(bytes(pixels), 9)) + chunk(b"IEND", b"")


if __name__ == "__main__":
    output = pathlib.Path(sys.argv[1])
    output.mkdir(parents=True, exist_ok=True)
    for name, size in {"avatar": (96, 96), "square": (160, 160), "wide": (320, 180)}.items():
        (output / f"{name}.png").write_bytes(image(*size))
    print("Synthetic showcase PNG generation: PASS (3 assets)")
