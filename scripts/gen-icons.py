"""Generate AgentHub icon assets from the robot-head logo geometry.

The frontend renders the same geometry as SVG in
frontend/src/components/AppLogo.vue; keep both in sync.

Usage: python scripts/gen-icons.py
"""

import io
import struct
from pathlib import Path

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parent.parent
BUILD = ROOT / "build"

HEAD = (30, 41, 59, 255)        # #1E293B
ACCENT = (16, 185, 129, 255)    # #10B981
BG_TOP = (255, 255, 255)
BG_BOTTOM = (237, 242, 245)


def _px(value: float, scale: float) -> float:
    return value * scale


def _gradient(size: int) -> Image.Image:
    column = Image.new("RGB", (1, size))
    for y in range(size):
        t = y / max(size - 1, 1)
        column.putpixel(
            (0, y),
            tuple(round(a + (b - a) * t) for a, b in zip(BG_TOP, BG_BOTTOM)),
        )
    return column.resize((size, size), Image.Resampling.BILINEAR)


def _draw_layers(draw: ImageDraw.ImageDraw, size: int, colored: bool) -> None:
    """Draw the robot. When colored is False only alpha-carrying black shapes
    are produced (macOS template icon)."""
    s = size / 1024
    body = HEAD if colored else (0, 0, 0, 255)
    accent = ACCENT if colored else (0, 0, 0, 255)

    # antenna
    draw.rounded_rectangle(
        [_px(492, s), _px(280, s), _px(532, s), _px(380, s)],
        radius=_px(20, s),
        fill=body,
    )
    draw.ellipse(
        [_px(458, s), _px(182, s), _px(566, s), _px(290, s)],
        fill=accent,
    )

    # head and ears
    draw.rounded_rectangle(
        [_px(248, s), _px(370, s), _px(776, s), _px(830, s)],
        radius=_px(116, s),
        fill=body,
    )
    draw.rounded_rectangle(
        [_px(184, s), _px(520, s), _px(232, s), _px(700, s)],
        radius=_px(24, s),
        fill=body,
    )
    draw.rounded_rectangle(
        [_px(792, s), _px(520, s), _px(840, s), _px(700, s)],
        radius=_px(24, s),
        fill=body,
    )


def _draw_face(draw: ImageDraw.ImageDraw, size: int, fill) -> None:
    """Eyes. For the template icon they are erased afterwards."""
    s = size / 1024
    for cx in (400, 624):
        draw.ellipse(
            [_px(cx - 74, s), _px(526, s), _px(cx + 74, s), _px(674, s)],
            fill=fill,
        )


def render(size: int, colored: bool = True, transparent: bool = False) -> Image.Image:
    """Render at 4x and downsample so every edge is antialiased."""
    scale = 4
    canvas_size = size * scale
    if transparent:
        image = Image.new("RGBA", (canvas_size, canvas_size), (0, 0, 0, 0))
    else:
        image = _gradient(canvas_size).convert("RGBA")
        draw = ImageDraw.Draw(image)
        corner = _px(160, canvas_size / 1024)
        # rounded corners: paint transparent outside the rounded rect
        mask = Image.new("L", (canvas_size, canvas_size), 0)
        ImageDraw.Draw(mask).rounded_rectangle(
            [0, 0, canvas_size - 1, canvas_size - 1], radius=corner, fill=255
        )
        image.putalpha(mask)
        draw = ImageDraw.Draw(image)

    draw = ImageDraw.Draw(image)
    _draw_layers(draw, canvas_size, colored)

    if colored:
        _draw_face(draw, canvas_size, ACCENT)
    else:
        # cut the face out of the silhouette so it reads as a robot
        cut = Image.new("L", (canvas_size, canvas_size), 0)
        _draw_face(ImageDraw.Draw(cut), canvas_size, 255)
        alpha = image.getchannel("A")
        alpha.paste(0, (0, 0), cut)
        image.putalpha(alpha)

    return image.resize((size, size), Image.Resampling.LANCZOS)


def png_bytes(image: Image.Image) -> bytes:
    buf = io.BytesIO()
    image.save(buf, format="PNG")
    return buf.getvalue()


def write_ico(path: Path, sizes: list[int]) -> None:
    images = [render(size, colored=True) for size in sizes]
    payloads = [png_bytes(image) for image in images]
    header = struct.pack("<HHH", 0, 1, len(images))
    offset = len(header) + 16 * len(images)
    entries = []
    for size, payload in zip(sizes, payloads):
        entries.append(
            struct.pack(
                "<BBBBHHII",
                0 if size >= 256 else size,
                0 if size >= 256 else size,
                0,
                0,
                1,
                32,
                len(payload),
                offset,
            )
        )
        offset += len(payload)
    path.write_bytes(header + b"".join(entries) + b"".join(payloads))


def write_icns(path: Path, targets: list[tuple[bytes, int]]) -> None:
    chunks = []
    for icon_type, size in targets:
        payload = png_bytes(render(size, colored=True))
        chunks.append(icon_type + struct.pack(">I", len(payload) + 8) + payload)
    body = b"".join(chunks)
    path.write_bytes(b"icns" + struct.pack(">I", len(body) + 8) + body)


def main() -> None:
    (BUILD / "windows").mkdir(parents=True, exist_ok=True)
    (BUILD / "darwin").mkdir(parents=True, exist_ok=True)

    render(1024).save(BUILD / "appicon.png")

    app_sizes = [16, 20, 24, 32, 40, 48, 64, 128, 256]
    write_ico(BUILD / "windows" / "icon.ico", app_sizes)
    write_ico(BUILD / "trayicon_windows.ico", app_sizes[:-1])

    icns_targets = [
        (b"icp4", 16),
        (b"icp5", 32),
        (b"icp6", 64),
        (b"ic07", 128),
        (b"ic08", 256),
        (b"ic09", 512),
        (b"ic10", 1024),
        (b"ic11", 32),
        (b"ic12", 64),
        (b"ic13", 256),
        (b"ic14", 512),
    ]
    write_icns(BUILD / "iconfile.icns", icns_targets)
    write_icns(BUILD / "darwin" / "iconfile.icns", icns_targets)

    # macOS menu bar wants a black template icon with a transparent background.
    template = render(44, colored=False, transparent=True).convert("RGBA")
    alpha = template.getchannel("A")
    template_l = Image.new("LA", template.size, (0, 255))
    template_l.putalpha(alpha)
    template_l.save(BUILD / "trayicon.png")


if __name__ == "__main__":
    main()
