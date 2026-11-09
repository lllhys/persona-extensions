import { readdir, lstat, readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
/** Stored DEFLATE blocks avoid zlib-version/runtime-dependent gzip bytes. */
function gzip(bytes) {
  const parts = [Buffer.from([0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 255])];
  const table = Array.from({ length: 256 }, (_, n) => {
    for (let bit = 0; bit < 8; bit++) n = n & 1 ? (n >>> 1) ^ 0xedb88320 : n >>> 1;
    return n >>> 0;
  });
  let crc = 0xffffffff;
  for (const byte of bytes) crc = (crc >>> 8) ^ table[(crc ^ byte) & 255];
  for (let at = 0; at < bytes.length; at += 65535) {
    const size = Math.min(65535, bytes.length - at), header = Buffer.alloc(5);
    header[0] = at + size === bytes.length ? 1 : 0;
    header.writeUInt16LE(size, 1); header.writeUInt16LE(size ^ 65535, 3);
    parts.push(header, bytes.subarray(at, at + size));
  }
  const footer = Buffer.alloc(8);
  footer.writeUInt32LE((crc ^ 0xffffffff) >>> 0, 0); footer.writeUInt32LE(bytes.length >>> 0, 4);
  parts.push(footer); return Buffer.concat(parts);
}
/** Deterministic POSIX ustar, without GNU tar flags or symlink traversal. */
export async function archiveTree(root, output) {
  const blocks = [];
  const octal = (header, at, size, n) => {
    const text = n.toString(8).padStart(size - 1, "0") + "\0";
    if (text.length !== size) throw new Error("Source archive exceeds ustar bounds");
    header.write(text, at, size, "ascii");
  };
  const visit = async (relative = "") => {
    for (const name of (await readdir(resolve(root, relative))).sort()) {
      const path = relative ? relative + "/" + name : name;
      const info = await lstat(resolve(root, path));
      if (!info.isDirectory() && !info.isFile()) throw new Error("Source archives contain only regular files and directories");
      const member = path + (info.isDirectory() ? "/" : "");
      if (Buffer.byteLength(member) > 100) throw new Error("Source archive path exceeds ustar name limit");
      const body = info.isFile() ? await readFile(resolve(root, path)) : Buffer.alloc(0), header = Buffer.alloc(512);
      header.write(member, 0, 100, "utf8");
      octal(header, 100, 8, info.isDirectory() ? 0o755 : 0o644);
      octal(header, 108, 8, 0); octal(header, 116, 8, 0); octal(header, 124, 12, body.length); octal(header, 136, 12, 0);
      header.fill(32, 148, 156); header.write(info.isDirectory() ? "5" : "0", 156);
      header.write("ustar\0", 257); header.write("00", 263);
      const checksum = header.reduce((sum, byte) => sum + byte, 0);
      header.write(checksum.toString(8).padStart(6, "0") + "\0 ", 148, 8, "ascii");
      blocks.push(header, body, Buffer.alloc((512 - body.length % 512) % 512));
      if (info.isDirectory()) await visit(path);
    }
  };
  await visit(); blocks.push(Buffer.alloc(1024));
  await writeFile(output, gzip(Buffer.concat(blocks)));
}
