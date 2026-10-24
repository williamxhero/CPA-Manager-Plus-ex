#!/usr/bin/env python3
"""Generate the CPAMP-EX fork badge across every CPAMP-owned brand asset.

The fork stamps a small "EX" corner chip onto the CPAMP brand assets so a
forbidden-edition build is visually distinct from upstream. This script is the
single source of truth for that transformation; the committed assets are its
output, never hand-edited.

Design
------
* Rounded-square chip, CPAMP primary blue ``#005CFF`` fill, white outline, bold
  white ``EX``, anchored to the bottom-right of the symbol region.
* Explicit canvas, symbol and protected text bounds cover each asset family;
  pure wordmarks are copied unchanged.
* The ICO is composed per frame (16/32/48) from the pre-badge 48px master, so
  each frame gets a badge sized for its own pixels instead of a shrunk 48px
  composition.
* A 16px frame cannot carry an anti-aliased Arial "EX" (the glyph lands under
  ~6px and smears to mush), so the 16px frame uses a bespoke 7x5 pixel glyph:
  a 3x5 ``E`` + 1px gap + 3x5 ``X`` drawn as whole white pixels on the blue
  chip. It stays a small corner tag -- it never fills the frame or hides the
  mark. The 32px and 48px frames keep the scalable glyph.

Reproducibility
---------------
Sources of truth are the *pre-badge* assets, read from a git ref (default
``DEFAULT_SOURCE_REF``, the last commit before the badge landed). Re-running the
script always rebuilds from pristine inputs, so the badge never compounds, and
the output is deterministic. Pass ``--check`` to regenerate in memory and fail
if the working tree has drifted from the generator.

Dependencies
------------
* Python 3.8+
* Pillow (``pip install Pillow``) -- PNG/ICO decode + compose.
* ``git`` on PATH (only when reading sources from a ref, i.e. not ``--source-dir``).

Usage
-----
    python bin/branding/generate-cpamp-ex-assets.py            # write outputs
    python bin/branding/generate-cpamp-ex-assets.py --check    # verify no drift
    python bin/branding/generate-cpamp-ex-assets.py --source-dir <dir>

Targets written (all paths relative to the repo root):
  apps/web/src/assets/brand/<name>.svg / <name>.png     (15 SVG + 15 PNG)
  apps/web/public/favicon.ico
  apps/web/public/apple-touch-icon.png
  apps/manager-server/internal/httpapi/web/favicon.ico          (byte-identical copy)
  apps/manager-server/internal/httpapi/web/apple-touch-icon.png (byte-identical copy)
  apps/web/index.html                                  (three inline base64 fallbacks)
"""

import argparse
import base64
import io
import os
import re
import struct
import subprocess
import sys
import xml.etree.ElementTree as ET

try:
    from PIL import Image, ImageDraw, ImageFont
    from PIL.PngImagePlugin import PngInfo
except ImportError:  # pragma: no cover - surfaced as a clean error
    sys.stderr.write("Pillow is required: pip install Pillow\n")
    raise

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

# Default git ref holding the pristine (pre-badge) CPAMP brand assets. It is the
# parent of the commit that first added the EX badge, so every run rebuilds from
# clean inputs instead of re-stamping an already-badged file.
DEFAULT_SOURCE_REF = "419c65fc"

FILL = (0, 92, 255, 255)
STROKE = (255, 255, 255, 255)
TEXT = (255, 255, 255, 255)
FONT_FALLBACKS = (
    "C:/Windows/Fonts/arialbd.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
    "/System/Library/Fonts/Supplemental/Arial Bold.ttf",
)

# Chip sizing (fractions of the shorter side).
ICON_FRAC = 0.30
LOCKUP_FRAC = 0.20
MARGIN_FRAC = 0.02
GLYPH_FRAC = 0.58
TEXT_MIN = 8.0

# 16px pixel glyph: 3x5 E + 1px gap + 3x5 X = 7x5 white pixels on the blue chip.
PIXEL_E = ("111", "100", "111", "100", "111")
PIXEL_X = ("101", "101", "010", "101", "101")
PIXEL_GLYPH_W = 7  # 3 + 1 + 3
PIXEL_GLYPH_H = 5
PIXEL_PAD = 1  # blue padding between the chip edge and the white glyph

BRAND_DIR = "apps/web/src/assets/brand"
FAVICON_REL = "apps/web/public/favicon.ico"
APPLE_REL = "apps/web/public/apple-touch-icon.png"
MSRV_DIR = "apps/manager-server/internal/httpapi/web"
INDEX_REL = "apps/web/index.html"
FAVICON_SVG_REL = f"{BRAND_DIR}/favicon.svg"

ASSET_LAYOUTS = {
    "horizontal": {
        "canvas": (12, 8, 1002, 302),
        "symbol": (12, 8, 272, 302),
        "text": (309, 8, 705, 302),
    },
    "vertical": {
        "canvas": (156.5, 50, 417, 425.34),
        "symbol": (237.119528, 50.000352, 255.759968, 283.968788),
        "text": (156.5, 395.889, 417, 79.451),
    },
    "symbol": {"canvas": (12, 8, 272, 302), "symbol": (12, 8, 272, 302)},
    "wordmark": {"canvas": (10, 8, 695, 123), "symbol": None, "text": (10, 8, 695, 123)},
    "app-icon": {"canvas": (0, 0, 1024, 1024), "symbol": (0, 0, 1024, 1024)},
}


def asset_layout(name):
    if name == "favicon.svg":
        return ASSET_LAYOUTS["symbol"]
    for category, layout in ASSET_LAYOUTS.items():
        prefix = f"cpamp-logo-{category}" if category in ("horizontal", "vertical") else f"cpamp-{category}"
        if name.startswith(prefix + "-") or name == prefix + ".svg":
            return layout
    raise ValueError(f"missing brand layout: {name}")


def raster_symbol_bounds(name, size):
    layout = asset_layout(name)
    cx, cy, cw, ch = layout["canvas"]
    sx, sy, sw, sh = layout["symbol"]
    w, h = size
    return (sx - cx) * w / cw, (sy - cy) * h / ch, sw * w / cw, sh * h / ch


def repo_root():
    # bin/branding/<this file> -> repo root is two levels up.
    return os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))


class SourceReader:
    """Read pre-badge asset bytes from a git ref or a plain directory."""

    def __init__(self, repo, ref=None, source_dir=None):
        self.repo = repo
        self.ref = ref
        self.source_dir = source_dir

    def bytes(self, rel):
        if self.source_dir:
            with open(os.path.join(self.source_dir, rel), "rb") as fh:
                return fh.read()
        if not self.ref:
            raise RuntimeError("no source ref or source dir configured")
        return subprocess.run(
            ["git", "-C", self.repo, "show", f"{self.ref}:{rel}"],
            check=True,
            capture_output=True,
        ).stdout

    def text(self, rel):
        return self.bytes(rel).decode("utf-8")

    def listdir(self, rel):
        if self.source_dir:
            return sorted(os.listdir(os.path.join(self.source_dir, rel)))
        out = subprocess.run(
            ["git", "-C", self.repo, "ls-tree", "--name-only", f"{self.ref}:{rel}"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout
        return sorted(line.strip() for line in out.splitlines() if line.strip())


# ---------------------------------------------------------------------------
# Badge drawing
# ---------------------------------------------------------------------------


def badge_box(w, h):
    shorter = min(w, h)
    ratio = max(w, h) / float(shorter)
    frac = LOCKUP_FRAC if ratio > 1.6 else ICON_FRAC
    s = shorter * frac
    m = shorter * MARGIN_FRAC
    return w - m - s, h - m - s, s


def fmt(v):
    return f"{v:.2f}".rstrip("0").rstrip(".")


def svg_badge(vx, vy, vw, vh):
    x0, y0, s = badge_box(vw, vh)
    x0 += vx
    y0 += vy
    cx = x0 + s / 2.0
    cy = y0 + s / 2.0
    r = s * 0.20
    sw = s * 0.07
    lines = [
        '  <g data-cpamp-ex="1" shape-rendering="geometricPrecision">',
        f'    <rect x="{fmt(x0)}" y="{fmt(y0)}" width="{fmt(s)}" height="{fmt(s)}" '
        f'rx="{fmt(r)}" fill="#005CFF" stroke="#FFFFFF" stroke-width="{fmt(sw)}"/>',
    ]
    if s >= TEXT_MIN:
        fs = s * GLYPH_FRAC
        lines.append(
            f'    <text x="{fmt(cx)}" y="{fmt(cy)}" font-family="Arial, Helvetica, sans-serif" '
            f'font-size="{fmt(fs)}" font-weight="700" fill="#FFFFFF" text-anchor="middle" '
            f'dominant-baseline="central">EX</text>'
        )
    lines.append("  </g>")
    return "\n" + "\n".join(lines) + "\n"


def patch_svg(src_text, name):
    root = ET.fromstring(src_text)
    if any(element.get("data-cpamp-ex") == "1" for element in root.iter()):
        raise ValueError(f"brand source already has an EX badge: {name}")
    text = src_text
    viewbox = root.get("viewBox", "").split()
    layout = asset_layout(name)
    if tuple(float(value) for value in viewbox) != layout["canvas"]:
        raise ValueError(f"brand canvas changed: {name}")
    if layout["symbol"] is None:
        return text
    sx, sy, sw, sh = layout["symbol"]
    bx, by, size = badge_box(sw, sh)
    half_stroke = size * 0.035
    left, top = sx + bx - half_stroke, sy + by - half_stroke
    right, bottom = sx + bx + size + half_stroke, sy + by + size + half_stroke
    cx, cy, cw, ch = layout["canvas"]
    if not (cx <= left < right <= cx + cw and cy <= top < bottom <= cy + ch):
        raise ValueError(f"brand badge would be clipped: {name}")
    if "text" in layout:
        tx, ty, tw, th = layout["text"]
        if left < tx + tw and right > tx and top < ty + th and bottom > ty:
            raise ValueError(f"brand badge would cover text: {name}")
    badge = svg_badge(*layout["symbol"])
    idx = text.rfind("</svg>")
    return text[:idx] + badge + text[idx:]


def load_font(size):
    for path in FONT_FALLBACKS:
        if os.path.exists(path):
            return ImageFont.truetype(path, size)
    return ImageFont.load_default()


def draw_badge(img, bounds=None):
    """Scalable badge for frames >= TEXT_MIN chip size (all non-ICO PNGs, 32/48)."""
    img = img.convert("RGBA")
    vx, vy, w, h = bounds if bounds is not None else (0, 0, *img.size)
    x0, y0, s = badge_box(w, h)
    x0 += vx
    y0 += vy
    overlay = Image.new("RGBA", img.size, (0, 0, 0, 0))
    d = ImageDraw.Draw(overlay)
    r = s * 0.20
    sw = max(1, round(s * 0.07))
    d.rounded_rectangle([x0, y0, x0 + s, y0 + s], radius=r, fill=FILL, outline=STROKE, width=sw)
    if s >= TEXT_MIN:
        fs = max(1, round(s * GLYPH_FRAC))
        d.text((x0 + s / 2.0, y0 + s / 2.0), "EX", font=load_font(fs), fill=TEXT, anchor="mm")
    return Image.alpha_composite(img, overlay)


def draw_pixel_badge(img):
    """Legible 7x5 pixel-glyph badge for the 16px ICO frame.

    A 3x5 ``E`` and 3x5 ``X`` separated by a 1px gap are stamped as whole white
    pixels on a blue chip with a 1px blue pad. The chip hugs the bottom-right
    corner and covers well under half the frame, so the mark stays readable.
    """
    img = img.convert("RGBA")
    w, h = img.size
    chip_w = PIXEL_GLYPH_W + 2 * PIXEL_PAD
    chip_h = PIXEL_GLYPH_H + 2 * PIXEL_PAD
    if w < chip_w or h < chip_h:
        raise RuntimeError(f"frame {w}x{h} too small for the pixel glyph")
    x0 = w - chip_w
    y0 = h - chip_h
    overlay = Image.new("RGBA", img.size, (0, 0, 0, 0))
    d = ImageDraw.Draw(overlay)
    d.rectangle([x0, y0, x0 + chip_w - 1, y0 + chip_h - 1], fill=FILL)
    for glyph, gx in ((PIXEL_E, x0 + PIXEL_PAD), (PIXEL_X, x0 + PIXEL_PAD + 4)):
        for ry, row in enumerate(glyph):
            for rx, bit in enumerate(row):
                if bit == "1":
                    d.point((gx + rx, y0 + PIXEL_PAD + ry), fill=TEXT)
    return Image.alpha_composite(img, overlay)


def png_bytes(img):
    buf = io.BytesIO()
    info = PngInfo()
    info.add_text("cpamp-ex", "1")
    img.save(buf, format="PNG", pnginfo=info, optimize=True)
    return buf.getvalue()


def build_ico(frames):
    count = len(frames)
    header = struct.pack("<HHH", 0, 1, count)
    entries = b""
    offset = 6 + 16 * count
    blobs = b""
    for size, data in frames:
        dim = 0 if size >= 256 else size
        entries += struct.pack("<BBBBHHII", dim, dim, 0, 0, 1, 32, len(data), offset)
        blobs += data
        offset += len(data)
    return header + entries + blobs


def patch_index_html(html, ico_data, apple_data, favicon_svg_bytes):
    ico_b64 = base64.b64encode(ico_data).decode()
    apple_b64 = base64.b64encode(apple_data).decode()
    fav_b64 = base64.b64encode(favicon_svg_bytes).decode()
    html, n1 = re.subn(
        r'(id="cpamp-favicon"[^>]*data-cpamp-fallback=")data:image/x-icon;base64,[^"]*(")',
        lambda m: m.group(1) + "data:image/x-icon;base64," + ico_b64 + m.group(2),
        html,
    )
    html, n2 = re.subn(
        r'(id="cpamp-apple-touch-icon"[^>]*data-cpamp-fallback=")data:image/png;base64,[^"]*(")',
        lambda m: m.group(1) + "data:image/png;base64," + apple_b64 + m.group(2),
        html,
    )
    html, n3 = re.subn(
        r'(rel="icon" type="image/svg\+xml" href=")data:image/svg\+xml;base64,[^"]*(")',
        lambda m: m.group(1) + "data:image/svg+xml;base64," + fav_b64 + m.group(2),
        html,
    )
    if (n1, n2, n3) != (1, 1, 1):
        raise RuntimeError(f"index.html fallback anchors not found exactly once: {(n1, n2, n3)}")
    return html


def build_outputs(repo, reader):
    """Return {relative_path: bytes} for every generated asset."""
    outputs = {}

    # 1. Brand SVGs from pristine, unbadged sources.
    svg_names = [n for n in reader.listdir(BRAND_DIR) if n.endswith(".svg")]
    for name in svg_names:
        text = patch_svg(reader.text(f"{BRAND_DIR}/{name}"), name)
        outputs[f"{BRAND_DIR}/{name}"] = text.encode("utf-8")

    # 2. Brand PNGs.
    for name in reader.listdir(BRAND_DIR):
        if not name.endswith(".png"):
            continue
        source = reader.bytes(f"{BRAND_DIR}/{name}")
        img = Image.open(io.BytesIO(source))
        if "cpamp-ex" in img.info:
            raise ValueError(f"brand source already has an EX badge: {name}")
        if asset_layout(name)["symbol"] is None:
            outputs[f"{BRAND_DIR}/{name}"] = source
            continue
        outputs[f"{BRAND_DIR}/{name}"] = png_bytes(draw_badge(img, raster_symbol_bounds(name, img.size)))

    # 3. favicon.ico -- per-frame composition from the pre-badge 48px master.
    master = Image.open(io.BytesIO(reader.bytes(FAVICON_REL)))
    base48 = master.ico.getimage((48, 48))
    if "cpamp-ex" in base48.info:
        raise ValueError("favicon source already has an EX badge")
    base48 = base48.convert("RGBA")
    frames = []
    for size in (16, 32, 48):
        frame = base48.resize((size, size), Image.LANCZOS)
        composited = draw_pixel_badge(frame) if size == 16 else draw_badge(frame)
        frames.append((size, png_bytes(composited)))
    ico_data = build_ico(frames)
    outputs[FAVICON_REL] = ico_data
    outputs[f"{MSRV_DIR}/favicon.ico"] = ico_data

    # 4. apple-touch-icon.png.
    apple = Image.open(io.BytesIO(reader.bytes(APPLE_REL)))
    if "cpamp-ex" in apple.info:
        raise ValueError("touch icon source already has an EX badge")
    apple_data = png_bytes(draw_badge(apple))
    outputs[APPLE_REL] = apple_data
    outputs[f"{MSRV_DIR}/apple-touch-icon.png"] = apple_data

    # 5. index.html inline base64 fallbacks.
    html = reader.text(INDEX_REL)
    outputs[INDEX_REL] = patch_index_html(
        html, ico_data, apple_data, outputs[FAVICON_SVG_REL]
    ).encode("utf-8")

    return outputs


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--repo", default=None, help="repo root (default: two levels above this script)")
    parser.add_argument("--source-ref", default=DEFAULT_SOURCE_REF, help="git ref with pristine assets")
    parser.add_argument("--source-dir", default=None, help="read pristine assets from a directory instead of git")
    parser.add_argument("--check", action="store_true", help="fail if the working tree differs from the generator output")
    args = parser.parse_args(argv)

    repo = os.path.abspath(args.repo) if args.repo else repo_root()
    reader = SourceReader(repo, ref=args.source_ref, source_dir=args.source_dir)
    try:
        outputs = build_outputs(repo, reader)
    except subprocess.CalledProcessError as exc:
        sys.stderr.write(f"git source read failed (ref {args.source_ref}): {exc}\n")
        return 2
    except (ValueError, ET.ParseError) as exc:
        sys.stderr.write(f"invalid brand source or layout: {exc}\n")
        return 2

    if args.check:
        drift = []
        for rel, data in sorted(outputs.items()):
            path = os.path.join(repo, rel)
            current = open(path, "rb").read() if os.path.exists(path) else None
            if current != data:
                drift.append(rel)
        if drift:
            sys.stderr.write("asset drift from generator:\n")
            for rel in drift:
                sys.stderr.write(f"  - {rel}\n")
            return 1
        print(f"OK: {len(outputs)} assets match the generator output")
        return 0

    for rel, data in outputs.items():
        path = os.path.join(repo, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as fh:
            fh.write(data)
    print(f"wrote {len(outputs)} assets -> {repo}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
