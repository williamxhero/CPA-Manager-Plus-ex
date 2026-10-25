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
        (self.brand / "favicon.svg").write_text(self.svg, encoding="utf-8")
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
        with redirect_stderr(io.StringIO()), redirect_stdout(io.StringIO()):
            return GENERATOR.main([
                "--repo", str(self.output),
                "--source-dir", str(self.source),
                *args,
            ])

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


if __name__ == "__main__":
    unittest.main()
