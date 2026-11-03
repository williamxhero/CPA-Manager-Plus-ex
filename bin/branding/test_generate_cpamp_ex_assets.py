import importlib.util
import io
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import patch

from PIL import Image
from PIL.PngImagePlugin import PngInfo

sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location(
    "brand_generator", Path(__file__).with_name("generate-cpamp-ex-assets.py")
)
GENERATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GENERATOR)

# Published M68 inventory, independent of the generator's enumeration.
SVG_FIXTURES = {
    **{f"cpamp-logo-horizontal-{color}.svg": "12 8 1002 302"
       for color in ("black", "color", "on-dark", "white")},
    **{f"cpamp-logo-vertical-{color}.svg": "156.5 50 417 425.34"
       for color in ("color", "on-dark")},
    **{f"cpamp-symbol-{color}.svg": "12 8 272 302"
       for color in ("black", "color", "white")},
    **{f"cpamp-wordmark-{color}.svg": "10 8 695 123"
       for color in ("black", "color", "on-dark", "white")},
    "cpamp-app-icon.svg": "0 0 1024 1024",
    "favicon.svg": "12 8 272 302",
}
PNG_FIXTURES = {
    **{f"cpamp-logo-horizontal-{color}-{width}.png": (width, height)
       for color in ("color", "on-dark") for width, height in ((240, 73), (480, 145), (960, 290))},
    **{f"cpamp-logo-vertical-{color}-{width}.png": (width, height)
       for color in ("color", "on-dark") for width, height in ((100, 102), (200, 204), (400, 408))},
    "cpamp-symbol-color.png": (90, 97),
    "cpamp-wordmark-color.png": (237, 48),
    "cpamp-wordmark-on-dark.png": (237, 48),
}
REQUIRED_SOURCE_RELS = tuple(
    f"apps/web/src/assets/brand/{name}" for name in (*SVG_FIXTURES, *PNG_FIXTURES)
) + ("apps/web/public/favicon.ico", "apps/web/public/apple-touch-icon.png", "apps/web/index.html")
EXPECTED_OUTPUT_RELS = REQUIRED_SOURCE_RELS + (
    "apps/manager-server/internal/httpapi/web/favicon.ico",
    "apps/manager-server/internal/httpapi/web/apple-touch-icon.png",
)


class BrandGeneratorTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.source = self.root / "source"
        self.output = self.root / "output"
        self.brand = self.source / GENERATOR.BRAND_DIR
        self.brand.mkdir(parents=True)
        self.svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="12 8 272 302"><path d="M22 18h20v20H22z"/></svg>'
        for name, canvas in SVG_FIXTURES.items():
            (self.brand / name).write_text(self.svg.replace("12 8 272 302", canvas), encoding="utf-8")
        for name, size in PNG_FIXTURES.items():
            Image.new("RGBA", size, "blue").save(self.brand / name)
        public = self.source / "apps/web/public"
        public.mkdir(parents=True)
        Image.new("RGBA", (48, 48), "blue").save(public / "favicon.ico", sizes=[(48, 48)])
        Image.new("RGBA", (180, 180), "blue").save(public / "apple-touch-icon.png")
        (self.source / GENERATOR.INDEX_REL).write_text(
            '<link id="cpamp-favicon" data-cpamp-fallback="data:image/x-icon;base64,old">'
            '<link id="cpamp-apple-touch-icon" data-cpamp-fallback="data:image/png;base64,old">'
            '<link rel="icon" type="image/svg+xml" href="data:image/svg+xml;base64,old">',
            encoding="utf-8",
        )

    def generate(self, *args):
        self.stderr = io.StringIO()
        with redirect_stderr(self.stderr), redirect_stdout(io.StringIO()):
            return GENERATOR.main([
                "--repo", str(self.output),
                "--source-dir", str(self.source),
                *args,
            ])

    def output_snapshot(self):
        return {
            p.relative_to(self.output).as_posix(): (p.read_bytes(), p.stat().st_mtime_ns)
            for p in self.output.rglob("*") if p.is_file()
        }

    def test_missing_required_sources_fail_closed_without_writing_or_trusting_outputs(self):
        self.assertEqual(self.generate(), 0)
        before = self.output_snapshot()
        self.assertEqual(set(before), set(EXPECTED_OUTPUT_RELS))
        for rel in REQUIRED_SOURCE_RELS:
            source = self.source / rel
            original = source.read_bytes()
            source.unlink()
            try:
                for args in ((), ("--check",)):
                    with self.subTest(missing=rel, args=args):
                        self.assertEqual(self.generate(*args), 2)
                        self.assertIn(source.name, self.stderr.getvalue())
                        self.assertEqual(self.output_snapshot(), before)
            finally:
                source.write_bytes(original)

    def test_rejects_badged_svg_regardless_of_xml_attribute_serialization(self):
        for marker in ('data-cpamp-ex="1"', "data-cpamp-ex='1'", "data-cpamp-ex = '&#49;'"):
            with self.subTest(marker=marker):
                (self.brand / "favicon.svg").write_text(
                    self.svg.replace("</svg>", f"<g {marker}></g></svg>"), encoding="utf-8"
                )
                self.assertEqual(self.generate(), 2)
                self.assertFalse(self.output.exists())

    def test_repeated_generation_and_check_preserve_outputs_without_git(self):
        wordmark = self.svg.replace("12 8 272 302", "10 8 695 123")
        (self.brand / "cpamp-wordmark-color.svg").write_text(wordmark, encoding="utf-8")
        Image.new("RGBA", (237, 48), "red").save(self.brand / "cpamp-wordmark-color.png")
        self.assertEqual(self.generate(), 0)
        before = {
            str(p.relative_to(self.output)): p.read_bytes()
            for p in self.output.rglob("*") if p.is_file()
        }
        self.assertEqual(set(before), {str(Path(rel)) for rel in EXPECTED_OUTPUT_RELS})
        self.assertEqual(self.generate(), 0)
        self.assertEqual(self.generate("--check"), 0)
        after = {
            str(p.relative_to(self.output)): p.read_bytes()
            for p in self.output.rglob("*") if p.is_file()
        }
        self.assertEqual(after, before)
        for name in ("cpamp-wordmark-color.svg", "cpamp-wordmark-color.png"):
            self.assertEqual(
                (self.output / GENERATOR.BRAND_DIR / name).read_bytes(),
                (self.brand / name).read_bytes(),
            )
        for name in ("favicon.ico", "apple-touch-icon.png"):
            self.assertEqual(
                (self.output / "apps/web/public" / name).read_bytes(),
                (self.output / GENERATOR.MSRV_DIR / name).read_bytes(),
            )
        drifted = self.output / GENERATOR.FAVICON_REL
        drifted.write_bytes(b"drifted")
        self.assertEqual(self.generate("--check"), 1)
        self.assertEqual(drifted.read_bytes(), b"drifted")

    def test_generates_valid_svg_with_closing_tag_whitespace(self):
        for closing in ("</svg >", "</svg\t>", "</svg\n>"):
            with self.subTest(closing=closing):
                (self.brand / "favicon.svg").write_text(
                    self.svg.replace("</svg>", closing), encoding="utf-8"
                )
                self.assertEqual(self.generate(), 0)
                result = (self.output / GENERATOR.FAVICON_SVG_REL).read_text(encoding="utf-8")
                root = ET.fromstring(result)
                self.assertEqual(sum(e.get("data-cpamp-ex") == "1" for e in root.iter()), 1)
                self.assertIn('<path d="M22 18h20v20H22z"/>', result)
                self.assertTrue(result.endswith(closing))

    def test_rejects_unsupported_svg_ending_without_writing_outputs(self):
        sources = (
            self.svg.replace("</svg>", "</svg><!-- closing-tag example: </svg> -->"),
            '<svg xmlns="http://www.w3.org/2000/svg" viewBox="12 8 272 302"/>',
        )
        for source in sources:
            with self.subTest(source=source):
                ET.fromstring(source)
                (self.brand / "favicon.svg").write_text(source, encoding="utf-8")
                self.assertEqual(self.generate(), 2)
                self.assertFalse(self.output.exists())

    def test_rejects_changed_canvas_without_writing_outputs(self):
        (self.brand / "favicon.svg").write_text(
            self.svg.replace("12 8 272 302", "12 8 273 302"), encoding="utf-8"
        )
        self.assertEqual(self.generate(), 2)
        self.assertFalse(self.output.exists())

    def test_rejects_malformed_svg_without_writing_outputs(self):
        (self.brand / "favicon.svg").write_text("<svg>", encoding="utf-8")
        self.assertEqual(self.generate(), 2)
        self.assertFalse(self.output.exists())

    def test_rejects_missing_asset_layout_without_writing_outputs(self):
        (self.brand / "unknown-brand.svg").write_text(self.svg, encoding="utf-8")
        self.assertEqual(self.generate(), 2)
        self.assertFalse(self.output.exists())

    def test_rejects_badged_png_without_writing_outputs(self):
        info = PngInfo()
        info.add_text("cpamp-ex", "1")
        Image.new("RGBA", (90, 97), "blue").save(
            self.brand / "cpamp-symbol-color.png", pnginfo=info
        )
        self.assertEqual(self.generate(), 2)
        self.assertFalse(self.output.exists())

    def test_rejects_badged_icons_without_changing_existing_outputs(self):
        self.assertEqual(self.generate(), 0)
        before = {
            str(p.relative_to(self.output)): p.read_bytes()
            for p in self.output.rglob("*") if p.is_file()
        }
        for name in ("favicon.ico", "apple-touch-icon.png"):
            with self.subTest(name=name):
                source = self.source / "apps/web/public" / name
                original = source.read_bytes()
                source.write_bytes((self.output / "apps/web/public" / name).read_bytes())
                self.assertEqual(self.generate(), 2)
                self.assertEqual({
                    str(p.relative_to(self.output)): p.read_bytes()
                    for p in self.output.rglob("*") if p.is_file()
                }, before)
                source.write_bytes(original)

    def test_rejects_clipped_and_overlapping_layout_metadata(self):
        original = GENERATOR.ASSET_LAYOUTS["symbol"]
        invalid_layouts = (
            {**original, "symbol": (1000, 1000, 272, 302)},
            {**original, "text": (12, 8, 272, 302)},
        )
        for layout in invalid_layouts:
            with self.subTest(layout=layout), patch.dict(GENERATOR.ASSET_LAYOUTS, {"symbol": layout}):
                self.assertEqual(self.generate(), 2)
                self.assertFalse(self.output.exists())

    def test_required_text_metadata_fails_closed_without_changing_outputs(self):
        self.assertEqual(self.generate(), 0)
        before = self.output_snapshot()
        for category in ("horizontal", "vertical", "wordmark"):
            original = GENERATOR.ASSET_LAYOUTS[category]
            invalid = (
                {key: value for key, value in original.items() if key != "text"},
                {**original, "text": None},
                {**original, "text": ()},
                {**original, "text": (0, 0, 0, 10)},
                {**original, "text": (float("nan"), 0, 10, 10)},
                {**original, "text": (0, 0, float("inf"), 10)},
            )
            for index, layout in enumerate(invalid):
                for args in ((), ("--check",)):
                    with self.subTest(category=category, invalid=index, args=args), patch.dict(
                        GENERATOR.ASSET_LAYOUTS, {category: layout}
                    ):
                        self.assertEqual(self.generate(*args), 2)
                        self.assertIn(category, self.stderr.getvalue())
                        self.assertIn("text", self.stderr.getvalue())
                        self.assertEqual(self.output_snapshot(), before)


if __name__ == "__main__":
    unittest.main()
