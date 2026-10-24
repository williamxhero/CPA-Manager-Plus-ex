import { execFileSync } from 'node:child_process';
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import zlib from 'node:zlib';
import { describe, expect, it } from 'vitest';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const brandDir = path.resolve(repoRoot, 'apps/web/src/assets/brand');
const indexPath = path.resolve(repoRoot, 'apps/web/index.html');
const faviconWeb = path.resolve(repoRoot, 'apps/web/public/favicon.ico');
const faviconServer = path.resolve(
  repoRoot,
  'apps/manager-server/internal/httpapi/web/favicon.ico'
);
const appleWeb = path.resolve(repoRoot, 'apps/web/public/apple-touch-icon.png');
const appleServer = path.resolve(
  repoRoot,
  'apps/manager-server/internal/httpapi/web/apple-touch-icon.png'
);

const SVG_MARKER = 'data-cpamp-ex="1"';
// Pillow PNG text chunks store the marker as plain bytes, so a raw scan suffices.
const PNG_MARKER = Buffer.from('cpamp-ex');
const read = (file) => readFileSync(file);
const CR = String.fromCharCode(13);

// --- Minimal, dependency-free ICO + PNG decoding ---------------------------
// The 16px favicon frame is a real PNG blob inside the ICO, so the guard can
// assert on the actual glyph pixels instead of a marker substring.

const ICO_PNG_SIGNATURE = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);

const parseIcoFrames = (buffer) => {
  const reserved = buffer.readUInt16LE(0);
  const type = buffer.readUInt16LE(2);
  const count = buffer.readUInt16LE(4);
  if (reserved !== 0 || type !== 1) throw new Error('not an ICO file');
  const frames = new Map();
  for (let i = 0; i < count; i += 1) {
    const entry = 6 + i * 16;
    const width = buffer.readUInt8(entry) || 256;
    const bytes = buffer.readUInt32LE(entry + 8);
    const offset = buffer.readUInt32LE(entry + 12);
    frames.set(width, buffer.subarray(offset, offset + bytes));
  }
  return frames;
};

const paethPredictor = (a, b, c) => {
  const p = a + b - c;
  const pa = Math.abs(p - a);
  const pb = Math.abs(p - b);
  const pc = Math.abs(p - c);
  if (pa <= pb && pa <= pc) return a;
  if (pb <= pc) return b;
  return c;
};

// Decodes an 8-bit RGBA, non-interlaced PNG (the only shape Pillow emits here).
const decodePng = (png) => {
  if (!png.subarray(0, 8).equals(ICO_PNG_SIGNATURE)) throw new Error('not a PNG blob');
  let cursor = 8;
  let header = null;
  const idat = [];
  while (cursor < png.length) {
    const length = png.readUInt32BE(cursor);
    const type = png.toString('latin1', cursor + 4, cursor + 8);
    const data = png.subarray(cursor + 8, cursor + 8 + length);
    if (type === 'IHDR') {
      header = {
        width: data.readUInt32BE(0),
        height: data.readUInt32BE(4),
        bitDepth: data.readUInt8(8),
        colorType: data.readUInt8(9),
        interlace: data.readUInt8(12),
      };
    } else if (type === 'IDAT') {
      idat.push(data);
    } else if (type === 'IEND') {
      break;
    }
    cursor += 12 + length;
  }
  if (!header) throw new Error('PNG missing IHDR');
  if (header.bitDepth !== 8 || header.colorType !== 6 || header.interlace !== 0) {
    throw new Error(`unsupported PNG shape: ${JSON.stringify(header)}`);
  }
  const { width, height } = header;
  const bpp = 4;
  const stride = width * bpp;
  const raw = zlib.inflateSync(Buffer.concat(idat));
  const out = Buffer.alloc(height * stride);
  let prev = Buffer.alloc(stride);
  for (let y = 0; y < height; y += 1) {
    const filter = raw[y * (stride + 1)];
    const line = raw.subarray(y * (stride + 1) + 1, y * (stride + 1) + 1 + stride);
    const cur = Buffer.alloc(stride);
    for (let x = 0; x < stride; x += 1) {
      const a = x >= bpp ? cur[x - bpp] : 0;
      const b = prev[x];
      const c = x >= bpp ? prev[x - bpp] : 0;
      let value = line[x];
      if (filter === 1) value = (value + a) & 0xff;
      else if (filter === 2) value = (value + b) & 0xff;
      else if (filter === 3) value = (value + ((a + b) >> 1)) & 0xff;
      else if (filter === 4) value = (value + paethPredictor(a, b, c)) & 0xff;
      cur[x] = value;
    }
    cur.copy(out, y * stride);
    prev = cur;
  }
  return { width, height, data: out };
};

const pixelAt = (img, x, y) => {
  const i = (y * img.width + x) * 4;
  return { r: img.data[i], g: img.data[i + 1], b: img.data[i + 2], a: img.data[i + 3] };
};
const isWhitePixel = (p) => p.r > 200 && p.g > 200 && p.b > 200 && p.a > 200;
const isBluePixel = (p) => p.b > 150 && p.b > p.r + 30 && p.a > 200;

// 16px pixel-glyph contract. Kept byte-for-byte in step with
// bin/branding/generate-cpamp-ex-assets.py (PIXEL_E / PIXEL_X / PIXEL_PAD).
const EX_PIXEL_E = ['111', '100', '111', '100', '111'];
const EX_PIXEL_X = ['101', '101', '010', '101', '101'];
const EX_PIXEL_PAD = 1;
const EX_GLYPH_W = 7; // 3 + 1 gap + 3
const EX_GLYPH_H = 5;
const EX_CHIP_W = EX_GLYPH_W + 2 * EX_PIXEL_PAD; // 9
const EX_CHIP_H = EX_GLYPH_H + 2 * EX_PIXEL_PAD; // 7

const brandSvgFiles = readdirSync(brandDir).filter((name) => name.endsWith('.svg'));
const brandPngFiles = readdirSync(brandDir).filter((name) => name.endsWith('.png'));

describe('CPAMP EX fork branding', () => {
  it('enumerates the full CPAMP brand asset set (svg + png)', () => {
    expect(brandSvgFiles.length).toBeGreaterThanOrEqual(15);
    expect(brandPngFiles.length).toBeGreaterThanOrEqual(15);
  });

  it.each(brandSvgFiles.filter((name) => name.includes('-logo-')))(
    'anchors the badge inside the symbol rather than the text in %s',
    (name) => {
      const svg = read(path.join(brandDir, name)).toString('utf8');
      const badge = svg.match(/<g data-cpamp-ex="1"[\s\S]*?<rect ([^>]+)>/);
      expect(badge).not.toBeNull();
      const attribute = (key) => Number(badge[1].match(new RegExp(`\\b${key}="([\\d.]+)"`))[1]);
      const x = attribute('x');
      const y = attribute('y');
      const width = attribute('width');
      const height = attribute('height');
      const stroke = attribute('stroke-width') / 2;
      const [left, top, right, bottom] = name.includes('-vertical-')
        ? [237.11, 50, 492.89, 333.98]
        : [12, 8, 284, 310];
      expect(x - stroke).toBeGreaterThanOrEqual(left);
      expect(y - stroke).toBeGreaterThanOrEqual(top);
      expect(x + width + stroke).toBeLessThanOrEqual(right);
      expect(y + height + stroke).toBeLessThanOrEqual(bottom);
      expect(width).toBeGreaterThan((right - left) * 0.28);
    }
  );

  it.each(brandPngFiles.filter((name) => name.includes('-logo-')))(
    'changes only the symbol region of raster %s',
    (name) => {
      const original = decodePng(
        execFileSync('git', ['show', `419c65fc:apps/web/src/assets/brand/${name}`], {
          cwd: repoRoot,
        })
      );
      const current = decodePng(read(path.join(brandDir, name)));
      expect([current.width, current.height]).toEqual([original.width, original.height]);
      const bounds = name.includes('-vertical-') ? [0.193, 0, 0.807, 0.668] : [0, 0, 0.272, 1];
      let changedInside = 0;
      let changedOutside = 0;
      for (let y = 0; y < current.height; y += 1) {
        for (let x = 0; x < current.width; x += 1) {
          const offset = (y * current.width + x) * 4;
          if (
            current.data
              .subarray(offset, offset + 4)
              .equals(original.data.subarray(offset, offset + 4))
          )
            continue;
          if (
            x >= bounds[0] * current.width &&
            x < bounds[2] * current.width &&
            y >= bounds[1] * current.height &&
            y < bounds[3] * current.height
          ) {
            changedInside += 1;
          } else {
            changedOutside += 1;
          }
        }
      }
      expect(changedInside).toBeGreaterThan(0);
      expect(changedOutside).toBe(0);
    }
  );

  it.each([...brandSvgFiles, ...brandPngFiles].filter((name) => name.includes('-wordmark-')))(
    'preserves pure wordmark %s without covering its letters',
    (name) => {
      const original = execFileSync('git', ['show', `419c65fc:apps/web/src/assets/brand/${name}`], {
        cwd: repoRoot,
      });
      const current = read(path.join(brandDir, name));
      if (name.endsWith('.svg')) {
        expect(current.toString('utf8').replaceAll(CR, '')).toBe(original.toString('utf8'));
      } else {
        expect(current.equals(original)).toBe(true);
      }
    }
  );

  it.each(brandSvgFiles.filter((name) => !name.includes('-wordmark-')))(
    'embeds one EX corner badge in %s',
    (name) => {
      expect(read(path.join(brandDir, name)).toString('utf8').split(SVG_MARKER)).toHaveLength(2);
    }
  );

  it.each(brandPngFiles.filter((name) => !name.includes('-wordmark-')))(
    'embeds the EX badge marker in %s',
    (name) => {
      expect(read(path.join(brandDir, name)).includes(PNG_MARKER)).toBe(true);
    }
  );

  it('keeps the favicon.ico EX build byte-identical between web and manager-server', () => {
    const web = read(faviconWeb);
    const server = read(faviconServer);
    expect(Buffer.compare(web, server)).toBe(0);
    expect(web.includes(PNG_MARKER)).toBe(true);
  });

  it('keeps the apple-touch-icon EX build byte-identical between web and manager-server', () => {
    const web = read(appleWeb);
    const server = read(appleServer);
    expect(Buffer.compare(web, server)).toBe(0);
    expect(web.includes(PNG_MARKER)).toBe(true);
  });

  it('binds every EX favicon/apple/svg fallback to the current asset bytes in index.html', () => {
    const html = read(indexPath).toString('utf8');
    const ico = read(faviconWeb).toString('base64');
    const apple = read(appleWeb).toString('base64');
    // favicon.svg is a text asset: the committed blob is LF, but a Windows checkout with
    // core.autocrlf may materialise CRLF on disk. Strip CR before hashing so the guard
    // compares the LF bytes that ship in the build, not the checkout's line endings.
    const svg = Buffer.from(
      read(path.join(brandDir, 'favicon.svg')).toString('utf8').split(CR).join(''),
      'utf8'
    ).toString('base64');

    expect(html).toContain(`data:image/x-icon;base64,${ico}`);
    expect(html).toContain(`data:image/png;base64,${apple}`);
    expect(html).toContain(`data:image/svg+xml;base64,${svg}`);
  });

  it('never stamps an EX marker onto third-party provider icons', () => {
    const iconsDir = path.resolve(repoRoot, 'apps/web/src/assets/icons');
    for (const name of readdirSync(iconsDir)) {
      if (name.startsWith('opencode-')) continue; // provider asset, still must be untouched
      const data = read(path.join(iconsDir, name));
      expect(data.includes(PNG_MARKER)).toBe(false);
      expect(data.toString('utf8').includes(SVG_MARKER)).toBe(false);
    }
  });

  it('uses the badged sidebar symbol without adding a second visual EX', () => {
    const layout = read(
      path.resolve(repoRoot, 'apps/web/src/components/layout/MainLayout.tsx')
    ).toString('utf8');
    expect(layout).toContain('src={CPAMP_SYMBOL_COLOR_PNG_URL}');
    expect(layout).not.toContain('className="sidebar-brand-ex"');
    expect(layout).toContain("alt={showSidebarLabels ? '' : 'CPA Manager Plus EX'}");

    const scss = read(path.resolve(repoRoot, 'apps/web/src/styles/layout.scss')).toString('utf8');
    expect(scss).not.toMatch(/\.sidebar-brand-ex\s*\{/);
  });

  it('keeps the baked EX chip large enough relative to icon-like assets', () => {
    // v1 used 0.26 * shorter side; the final acceptance round required a bigger,
    // bolder corner chip so it still resolves at small render sizes.
    const symbol = read(path.join(brandDir, 'cpamp-symbol-color.svg')).toString('utf8');
    const rect = symbol.match(/<rect x="([\d.]+)"[^>]*?\bwidth="([\d.]+)"/);
    expect(rect).not.toBeNull();
    const [, , width] = rect;
    // cpamp-symbol-color.svg viewBox is 272 wide (shorter side).
    expect(Number(width)).toBeGreaterThanOrEqual(272 * 0.28);
  });
});

describe('CPAMP EX 16px favicon pixel glyph', () => {
  const frames = parseIcoFrames(read(faviconWeb));
  const frame16 = decodePng(frames.get(16));

  it('decodes the 16x16 ICO frame down to RGBA pixels', () => {
    expect(frame16.width).toBe(16);
    expect(frame16.height).toBe(16);
  });

  it('draws EX as whole white pixels on the blue corner chip', () => {
    // Glyph origin: bottom-right chip (9x7) with a 1px blue pad.
    const gx = frame16.width - EX_CHIP_W + EX_PIXEL_PAD; // 8
    const gy = frame16.height - EX_CHIP_H + EX_PIXEL_PAD; // 10
    for (const [glyph, offset] of [
      [EX_PIXEL_E, 0],
      [EX_PIXEL_X, 4], // 3px E + 1px gap
    ]) {
      for (let ry = 0; ry < glyph.length; ry += 1) {
        for (let rx = 0; rx < glyph[ry].length; rx += 1) {
          const point = `(${gx + offset + rx},${gy + ry})`;
          if (glyph[ry][rx] === '1') {
            expect(isWhitePixel(pixelAt(frame16, gx + offset + rx, gy + ry)), point).toBe(true);
          } else {
            expect(isBluePixel(pixelAt(frame16, gx + offset + rx, gy + ry)), point).toBe(true);
          }
        }
      }
    }
    // The 1px separator column between E and X stays blue.
    for (let ry = 0; ry < EX_GLYPH_H; ry += 1) {
      expect(isBluePixel(pixelAt(frame16, gx + 3, gy + ry))).toBe(true);
    }
    // Padding rows/cols around the glyph stay blue (the glyph floats on blue).
    for (let x = frame16.width - EX_CHIP_W; x < frame16.width; x += 1) {
      expect(isBluePixel(pixelAt(frame16, x, frame16.height - EX_CHIP_H))).toBe(true);
    }
  });

  it('keeps EX as a corner tag rather than replacing the frame', () => {
    // Top corners transparent: the frame is not a full EX tile.
    expect(pixelAt(frame16, 0, 0).a).toBeLessThan(40);
    expect(pixelAt(frame16, 15, 0).a).toBeLessThan(40);
    // The upper half still carries the CPAMP diamond, so the mark survives.
    let blueTop = 0;
    let whitePixels = 0;
    for (let y = 0; y < frame16.height; y += 1) {
      for (let x = 0; x < frame16.width; x += 1) {
        if (y < 9 && isBluePixel(pixelAt(frame16, x, y))) blueTop += 1;
        if (isWhitePixel(pixelAt(frame16, x, y))) whitePixels += 1;
      }
    }
    expect(blueTop).toBeGreaterThan(20);
    // Exactly the 20 glyph pixels are white (11 E + 9 X) -- the whole point.
    expect(whitePixels).toBe(20);
    // The chip covers a minority of the frame (well under half).
    expect(EX_CHIP_W * EX_CHIP_H).toBeLessThan(frame16.width * frame16.height * 0.3);
  });

  it('keeps the 32px and 48px frames as larger separate compositions', () => {
    const frame32 = decodePng(frames.get(32));
    const frame48 = decodePng(frames.get(48));
    expect(frame32.width).toBe(32);
    expect(frame48.width).toBe(48);
    // Each still carries its baked accent chip near the bottom-right corner.
    expect(isBluePixel(pixelAt(frame32, frame32.width - 3, frame32.height - 3))).toBe(true);
    expect(isBluePixel(pixelAt(frame48, frame48.width - 3, frame48.height - 3))).toBe(true);
  });

  it('ships the reproducible generator and keeps the glyph contract in step', () => {
    const script = read(
      path.resolve(repoRoot, 'bin/branding/generate-cpamp-ex-assets.py')
    ).toString('utf8');
    // Present so the committed pixels can always be rebuilt from pristine sources.
    expect(script).toContain('DEFAULT_SOURCE_REF');
    expect(script).toContain('def draw_pixel_badge');
    expect(script).toContain('PIXEL_E = ("111", "100", "111", "100", "111")');
    expect(script).toContain('PIXEL_X = ("101", "101", "010", "101", "101")');
  });
});
