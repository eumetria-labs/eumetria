// A minimal ZIP writer: stored (uncompressed) entries with UTF-8 names,
// enough for the export. No DOM access, so Node can test this file.

const CRC = new Uint32Array(256).map((_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

export function crc32(bytes) {
  let c = 0xffffffff;
  for (const b of bytes) c = CRC[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

// zip returns the archive of files, [name, text] pairs, as bytes; the
// entries carry the local time of now.
export function zip(files, now = new Date()) {
  const enc = new TextEncoder();
  const time = (now.getHours() << 11) | (now.getMinutes() << 5) | (now.getSeconds() >> 1);
  const date = ((now.getFullYear() - 1980) << 9) | ((now.getMonth() + 1) << 5) | now.getDate();
  const parts = [], central = [];
  let offset = 0;
  for (const [name, text] of files) {
    const n = enc.encode(name), data = enc.encode(text), crc = crc32(data);
    // Version 2.0, UTF-8 names (bit 11), stored, time, date, CRC, sizes, name length.
    const fields = (v) => { v.setUint16(0, 20, true); v.setUint16(2, 0x800, true); v.setUint16(4, 0, true); v.setUint16(6, time, true); v.setUint16(8, date, true); v.setUint32(10, crc, true); v.setUint32(14, data.length, true); v.setUint32(18, data.length, true); v.setUint16(22, n.length, true); };
    const local = new DataView(new ArrayBuffer(30));
    local.setUint32(0, 0x04034b50, true);
    fields(new DataView(local.buffer, 4));
    const dir = new DataView(new ArrayBuffer(46));
    dir.setUint32(0, 0x02014b50, true);
    dir.setUint16(4, 20, true); // made by
    fields(new DataView(dir.buffer, 6));
    dir.setUint32(42, offset, true);
    parts.push(new Uint8Array(local.buffer), n, data);
    central.push(new Uint8Array(dir.buffer), n);
    offset += 30 + n.length + data.length;
  }
  const size = central.reduce((a, p) => a + p.length, 0);
  const end = new DataView(new ArrayBuffer(22));
  end.setUint32(0, 0x06054b50, true);
  end.setUint16(8, files.length, true);
  end.setUint16(10, files.length, true);
  end.setUint32(12, size, true);
  end.setUint32(16, offset, true);
  const all = [...parts, ...central, new Uint8Array(end.buffer)];
  const out = new Uint8Array(all.reduce((a, p) => a + p.length, 0));
  let i = 0;
  for (const p of all) { out.set(p, i); i += p.length; }
  return out;
}
