// Images on their way to the server: drawn into a canvas at most side px on
// the long side, which also turns a phone's photo the right way up, and sent
// as base64 in JSON like every other change. The server decodes them again
// and takes nothing from the file but its pixels.

import { h } from './dom.js';

export async function imageForUpload(file, side) {
  const bitmap = await createImageBitmap(file);
  const k = Math.min(1, side / Math.max(bitmap.width, bitmap.height));
  const canvas = h('canvas', { width: Math.round(bitmap.width * k), height: Math.round(bitmap.height * k) });
  const ctx = canvas.getContext('2d');
  ctx.fillStyle = '#fff'; // a JPEG has no transparency
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  const blob = await new Promise((ok) => canvas.toBlob(ok, 'image/jpeg', 0.85));
  if (!blob) throw new Error('the browser could not encode the image');
  return new Promise((ok, fail) => {
    const r = new FileReader();
    r.onload = () => ok(r.result.slice(r.result.indexOf(',') + 1));
    r.onerror = () => fail(r.error);
    r.readAsDataURL(blob);
  });
}
