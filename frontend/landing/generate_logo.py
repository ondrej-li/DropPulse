#!/usr/bin/env python3
"""Generate the 120x120 DropPulse PNG logo.

The logo renders the DropPulse pulse wave mark centered inside a rounded-corner
squircle with the brand's teal-to-aquamarine gradient (#0f766e to #14b8a6) and
anti-aliased 8x supersampling on a transparent background.

    python3 frontend/landing/generate_logo.py
"""

from __future__ import annotations

import math
import struct
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1] / "public"
OUTPUT_PATH = ROOT / "logo.png"

W, H = 120, 120
SCALE = 120.0 / 64.0
R = 14.0 * SCALE
STROKE_W = 5.0 * SCALE
STROKE_R = STROKE_W / 2.0

POINTS = [
    (14.0 * SCALE, 34.0 * SCALE),
    (22.0 * SCALE, 34.0 * SCALE),
    (27.0 * SCALE, 22.0 * SCALE),
    (35.0 * SCALE, 46.0 * SCALE),
    (40.0 * SCALE, 34.0 * SCALE),
    (50.0 * SCALE, 34.0 * SCALE),
]


def dist_segment(px: float, py: float, x1: float, y1: float, x2: float, y2: float) -> float:
    dx, dy = x2 - x1, y2 - y1
    l2 = dx * dx + dy * dy
    if l2 == 0:
        return math.hypot(px - x1, py - y1)
    t = max(0.0, min(1.0, ((px - x1) * dx + (py - y1) * dy) / l2))
    return math.hypot(px - (x1 + t * dx), py - (y1 + t * dy))


def dist_polyline(px: float, py: float) -> float:
    return min(
        dist_segment(px, py, POINTS[i][0], POINTS[i][1], POINTS[i + 1][0], POINTS[i + 1][1])
        for i in range(len(POINTS) - 1)
    )


def rounded_rect_sdf(px: float, py: float) -> float:
    qx = abs(px - W / 2.0) - (W / 2.0 - R)
    qy = abs(py - H / 2.0) - (H / 2.0 - R)
    ax, ay = max(qx, 0.0), max(qy, 0.0)
    return math.hypot(ax, ay) + min(max(qx, qy), 0.0) - R


def generate_logo_rgba() -> bytearray:
    ss = 8
    rgba = bytearray(W * H * 4)

    for y in range(H):
        for x in range(W):
            bg_alpha = 0
            fg_alpha = 0
            t_diag = (x + y) / (W + H)
            r_bg = int(15 + (20 - 15) * t_diag)
            g_bg = int(118 + (184 - 118) * t_diag)
            b_bg = int(110 + (166 - 110) * t_diag)

            for sy in range(ss):
                py = y + (sy + 0.5) / ss
                for sx in range(ss):
                    px = x + (sx + 0.5) / ss
                    if rounded_rect_sdf(px, py) <= 0:
                        bg_alpha += 1
                        if dist_polyline(px, py) <= STROKE_R:
                            fg_alpha += 1

            bg_cov = bg_alpha / (ss * ss)
            fg_cov = fg_alpha / (ss * ss)

            if bg_cov > 0:
                cr = r_bg * (1.0 - fg_cov) + 255.0 * fg_cov
                cg = g_bg * (1.0 - fg_cov) + 255.0 * fg_cov
                cb = b_bg * (1.0 - fg_cov) + 255.0 * fg_cov
                ca = 255.0 * bg_cov
            else:
                cr, cg, cb, ca = 0.0, 0.0, 0.0, 0.0

            idx = (y * W + x) * 4
            rgba[idx] = int(cr)
            rgba[idx + 1] = int(cg)
            rgba[idx + 2] = int(cb)
            rgba[idx + 3] = int(ca)

    return rgba


def write_png(width: int, height: int, rgba: bytearray, path: Path) -> None:
    raw = bytearray()
    for row in range(height):
        raw.append(0)
        raw.extend(rgba[row * width * 4 : (row + 1) * width * 4])

    def chunk(tag: bytes, payload: bytes) -> bytes:
        return (
            struct.pack(">I", len(payload))
            + tag
            + payload
            + struct.pack(">I", zlib.crc32(tag + payload) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    idat = zlib.compress(bytes(raw), 9)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "wb") as f:
        f.write(
            b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", idat)
            + chunk(b"IEND", b"")
        )


def main() -> int:
    rgba = generate_logo_rgba()
    write_png(W, H, rgba, OUTPUT_PATH)
    print(f"wrote {OUTPUT_PATH.relative_to(ROOT.parent.parent)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
