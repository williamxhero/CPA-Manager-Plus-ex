import { createHash } from 'node:crypto';
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
const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');
// Published M68 baseline names; extra classified assets may not replace these.
const requiredBrandSvgFiles = [
  'cpamp-app-icon.svg',
  'cpamp-logo-horizontal-black.svg',
  'cpamp-logo-horizontal-color.svg',
  'cpamp-logo-horizontal-on-dark.svg',
  'cpamp-logo-horizontal-white.svg',
  'cpamp-logo-vertical-color.svg',
  'cpamp-logo-vertical-on-dark.svg',
  'cpamp-symbol-black.svg',
  'cpamp-symbol-color.svg',
  'cpamp-symbol-white.svg',
  'cpamp-wordmark-black.svg',
  'cpamp-wordmark-color.svg',
  'cpamp-wordmark-on-dark.svg',
  'cpamp-wordmark-white.svg',
  'favicon.svg',
];
const requiredBrandPngFiles = [
  'cpamp-logo-horizontal-color-240.png',
  'cpamp-logo-horizontal-color-480.png',
  'cpamp-logo-horizontal-color-960.png',
  'cpamp-logo-horizontal-on-dark-240.png',
  'cpamp-logo-horizontal-on-dark-480.png',
  'cpamp-logo-horizontal-on-dark-960.png',
  'cpamp-logo-vertical-color-100.png',
  'cpamp-logo-vertical-color-200.png',
  'cpamp-logo-vertical-color-400.png',
  'cpamp-logo-vertical-on-dark-100.png',
  'cpamp-logo-vertical-on-dark-200.png',
  'cpamp-logo-vertical-on-dark-400.png',
  'cpamp-symbol-color.png',
  'cpamp-wordmark-color.png',
  'cpamp-wordmark-on-dark.png',
];
// Git-blob SHA-256 at d680c9d0b7deeba8d54b4c81849d63e8135b1221.
// Normalize only SVG checkout CRLFs; preserve lone CRs and byte-exact PNGs.
// Static literals keep source archives verifiable without Git history.
const providerIconHashes = {
  'antigravity.svg': '85c2caf68a95d71cb810f4225ae338861fedbd2bc21b160883df94f0065f4d7f',
  'claude.svg': 'a3101f3047a119aa11825ad9369510f0c472428c8c52d420e31bc62db44a8364',
  'codex.svg': 'e98c06def82d8d5c33d84a3589c85bec21ca652695bb8e6c60d2b7d238b8885e',
  'deepseek.svg': 'deba5f98a5c1796e20fcac3149bcd7eb8a32f0bdd04d048819400b1f28bd1439',
  'devin-dark.svg': '3254924b008c93287601afee2d20cc4afbcbea038872f941face3099b1db70d6',
  'devin.svg': 'a924e172895a638b1e104ab784054f7220437d172cdff3bf08c94ef696a64ad4',
  'gemini.svg': 'd7161ea5a85c51d996bb7611c665e9b531db5a4092d4978ed4d6ddf6c043257d',
  'glm.svg': '8174e65ff71647d5c705c920c953a82bd723d9e3e42887bdd4ffc9dfe51207a0',
  'grok-dark.svg': '1d4625472528090d3b4f788136cbd10468a9bf494193eb1e92d67e7d011b42cf',
  'grok.svg': '9175fc90c22655160231976c849f25a03b888d7cc0e04c5f1b987b659bb07c95',
  'iflow.svg': '7450cab608ce761bfe5d5f5c95c124788e1ef7f1741f7ca3f4b3dbf71e28abcd',
  'kimi-dark.svg': 'f95bbaca97a975b2154c729c5128fed366fa076cc606aa505c4b78fcbe9ebc33',
  'kimi-light.svg': '952b010de7104acdb62ac2238453bef628ca72b3753151ca05d74c7815c70647',
  'meta.svg': 'e688b9947034e5a1a6e43435da3cd5eeb0d1102290679903dc96c6d43447a99b',
  'minimax.svg': '553256ae1bb6ea442649b8229a4a2e6e5a7e2008404ebd8788b06343fb5c7c1c',
  'openai-dark.svg': '6f256aa9fa2c5ec28869a78cc904ab6026728fdfdcb7254d1f877fd348736386',
  'openai-light.svg': '443edb601fc09f41e1fb091421f971f56ab244a52b34de4d4af28b43c6000433',
  'opencode-dark.png': '20c8b4794f4c988ab335a519980415ba94ea3442b5a794b920c5408be295bc53',
  'opencode-light.png': '0181fe611c6cd686f7d06333087b93fd8bd4a9dd54dd1df5a3fc6b3c86147f3c',
  'qwen.svg': '0aa9e2cc95b36f0243cbf384043ae80a2e32542e9260ded335dd59de5e625cb5',
  'vertex.svg': 'd6a643fad5de9e513bba05af22b685572e5e06b92c3e5495b7ecf56666ff79c0',
};
const iconsDir = path.resolve(repoRoot, 'apps/web/src/assets/icons');
const assertProviderIconUnchanged = (name, data) => {
  const bytes = name.endsWith('.svg')
    ? Buffer.from(data.toString('utf8').replaceAll('\r\n', '\n'), 'utf8')
    : data;
  expect(sha256(bytes), `third-party provider bytes changed: ${name}`).toBe(providerIconHashes[name]);
  expect(data.includes(PNG_MARKER)).toBe(false);
  expect(data.toString('utf8').includes(SVG_MARKER)).toBe(false);
};

// Pristine 419c65fc pixel/wordmark fingerprints keep source archives testable without Git history.
const pristineHashes = {
  'cpamp-logo-horizontal-color-240.png':
    '850936d5dc383926ab5e1450a3b66b97c98f3f48c7d6fac336d4654b9aceabfe',
  'cpamp-logo-horizontal-color-480.png':
    '9584975cd09c60a193095102f577feec0448281136a89350aed66fccedee8c62',
  'cpamp-logo-horizontal-color-960.png':
    '2d52289741ea5c2d4d8ac1a10d2dbee10873eb9e34106a79185fff52dc08957e',
  'cpamp-logo-horizontal-on-dark-240.png':
    'a9a5fa7af28424d333eb0262950001fd02dd39b778800abf9082829d4459fc12',
  'cpamp-logo-horizontal-on-dark-480.png':
    '29e761432a5c1316e46d1f785e926f345cd156ef6d009b925a00d19b4e52b2fa',
  'cpamp-logo-horizontal-on-dark-960.png':
    '9d3202317f53fc4caa8eeeb63c4049a8bc947fd49690afaf576a3a20ab013f42',
  'cpamp-logo-vertical-color-100.png':
    '4eaa56f1fb71d9337e47f76622d1a0f0421aad02e1e99eb58d77c3f19590df2a',
  'cpamp-logo-vertical-color-200.png':
    '51b409a9859c7909b768abd794a1aa48992d1c99e51dfcd979ac0ff6ae980b47',
  'cpamp-logo-vertical-color-400.png':
    'f4a6f599f19430461512a76dc4a181ddce90283e488e08a5749e67aa8fac1bcb',
  'cpamp-logo-vertical-on-dark-100.png':
    'c94062ac586c6f5a84f12751d5b1f4dd6f5d6c942b854b0f3a50d3a9ac4549b4',
  'cpamp-logo-vertical-on-dark-200.png':
    'bf3b131bd941dbc577628366e35f78befb518c3db2a771283a319200db3821a0',
  'cpamp-logo-vertical-on-dark-400.png':
    '306d8015d1051c1248c722deb0956c888604a2869dd534d6cd8c7ed97fe4c655',
  'cpamp-wordmark-black.svg': '347f017a3951924bea262fd73690c20b7f605d26fa083f66f5b71650ea5d872a',
  'cpamp-wordmark-color.png': 'ed8142fbc32812668b6ed0dd45b37c436f36d19f260898a02dade4afed814ce5',
  'cpamp-wordmark-color.svg': 'f74f3812ec1d18ede9925bb2ddfa02bea783539ae02ac74122fd4b9950a455fd',
  'cpamp-wordmark-on-dark.png': '037953906fdc0d17115c821b9acebbcc522f3728f4c1852046a14743021220be',
  'cpamp-wordmark-on-dark.svg': '615e28dcb3b49e5e9091a0de19b7fd612b416b4020c690f4a01d55fa3cef7641',
  'cpamp-wordmark-white.svg': '1bbe84eed98bbaf8bb979d652f76922ace7bd20dfaab9c3fc5abbe4f3bb94bdc',
};

describe('CPAMP EX fork branding', () => {
  it('enumerates the full CPAMP brand asset set (svg + png)', () => {
    expect(brandSvgFiles.length).toBeGreaterThanOrEqual(15);
    expect(brandPngFiles.length).toBeGreaterThanOrEqual(15);
    expect(brandSvgFiles).toEqual(expect.arrayContaining(requiredBrandSvgFiles));
    expect(brandPngFiles).toEqual(expect.arrayContaining(requiredBrandPngFiles));
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
      const current = decodePng(read(path.join(brandDir, name)));
      const bounds = name.includes('-vertical-') ? [0.193, 0, 0.807, 0.668] : [0, 0, 0.272, 1];
      const protectedPixels = [];
      for (let y = 0; y < current.height; y += 1) {
        for (let x = 0; x < current.width; x += 1) {
          if (
            x >= bounds[0] * current.width &&
            x < bounds[2] * current.width &&
            y >= bounds[1] * current.height &&
            y < bounds[3] * current.height
          )
            continue;
          const offset = (y * current.width + x) * 4;
          protectedPixels.push(current.data.subarray(offset, offset + 4));
        }
      }
      expect(sha256(Buffer.concat(protectedPixels))).toBe(pristineHashes[name]);
    }
  );

  it.each([...brandSvgFiles, ...brandPngFiles].filter((name) => name.includes('-wordmark-')))(
    'preserves pure wordmark %s without covering its letters',
    (name) => {
      const current = read(path.join(brandDir, name));
      const bytes = name.endsWith('.svg')
        ? Buffer.from(current.toString('utf8').replaceAll(CR, ''), 'utf8')
        : current;
      expect(sha256(bytes)).toBe(pristineHashes[name]);
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
    for (const name of readdirSync(iconsDir)) {
      assertProviderIconUnchanged(name, read(path.join(iconsDir, name)));
    }
  });

  it('covers every third-party provider asset with a fixed byte baseline', () => {
    expect(readdirSync(iconsDir).sort()).toEqual(Object.keys(providerIconHashes).sort());
  });

  it('accepts a CRLF checkout of a third-party SVG without changing its content', () => {
    const name = 'antigravity.svg';
    const lf = read(path.join(iconsDir, name)).toString('utf8').replaceAll('\r\n', '\n');
    const crlf = Buffer.from(lf.replaceAll('\n', '\r\n'), 'utf8');
    expect(crlf.includes(Buffer.from('\r\n'))).toBe(true);
    expect(sha256(crlf)).not.toBe(providerIconHashes[name]);
    expect(() => assertProviderIconUnchanged(name, crlf)).not.toThrow();
  });

  it('rejects a lone CR inside a third-party SVG attribute value', () => {
    const name = 'antigravity.svg';
    const original = read(path.join(iconsDir, name)).toString('utf8').replaceAll('\r\n', '\n');
    expect(original).toContain('fill="#3789F9"');
    const changed = Buffer.from(original.replace('fill="#3789F9"', `fill="#3${CR}789F9"`), 'utf8');
    expect(changed.toString('utf8')).toContain(`fill="#3${CR}789F9"`);
    expect(sha256(changed)).toBe('7e8364b33f2fccf254f6af87b77f92edd5c679a35a8182853471ebba33ab1e97');
    expect(() => assertProviderIconUnchanged(name, changed)).toThrow();
  });

  it.each(['opencode-dark.png', 'opencode-light.png'])(
    'keeps byte-exact PNG protection when carriage-return bytes change in %s',
    (name) => {
      const original = read(path.join(iconsDir, name));
      const changed = Buffer.concat([original.subarray(0, 4), Buffer.from(CR), original.subarray(4)]);
      expect(() => assertProviderIconUnchanged(name, changed)).toThrow();
    }
  );

  it.each(Object.keys(providerIconHashes))(
    'rejects a non-EX byte change to third-party provider %s',
    (name) => {
      // In-memory corruption only: never mutate the actual provider asset.
      const changed = Buffer.from(read(path.join(iconsDir, name)));
      changed[0] ^= 1;
      expect(() => assertProviderIconUnchanged(name, changed)).toThrow();
    }
  );

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
