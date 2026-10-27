#!/usr/bin/env python3
"""Crop the empty margin off the generated organization marks.

The model renders each mark small on a large off-white field (the prompt asks for generous margin),
so at 22px in a table row the visible glyph is only ~12px and reads as a smudge. This finds the
bounding box of the actual mark, squares it with a little breathing room, and crops to it — no
padding is synthesised, so no seam can appear against the mark's own (slightly off-white) field.

Pure stdlib: parses the PNG, unfilters the scanlines, scans for pixels that differ from the corner
pixel's colour. No PIL/ImageMagick on this box.
"""

import glob
import os
import struct
import subprocess
import sys
import zlib

FFMPEG = os.environ.get("FFMPEG", "ffmpeg")
TOLERANCE = 16      # how far a channel must differ from the background before it counts as content
PADDING = 0.10      # breathing room around the content box, as a fraction of its longest side
SIZE = 128          # committed size (must match scripts/generate-mock-assets.mjs)


def read_png(path):
    data = open(path, "rb").read()
    if data[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError(f"{path}: not a PNG")
    pos, idat = 8, b""
    width = height = depth = colour = interlace = None
    while pos < len(data):
        length = struct.unpack(">I", data[pos : pos + 4])[0]
        kind = data[pos + 4 : pos + 8]
        chunk = data[pos + 8 : pos + 8 + length]
        pos += 12 + length
        if kind == b"IHDR":
            width, height, depth, colour, _comp, _filt, interlace = struct.unpack(">IIBBBBB", chunk)
        elif kind == b"IDAT":
            idat += chunk
        elif kind == b"IEND":
            break
    if depth != 8 or colour not in (2, 6) or interlace != 0:
        raise ValueError(f"{path}: unsupported PNG (depth={depth} colour={colour} interlace={interlace})")
    if width is None or height is None:
        raise ValueError(f"{path}: no IHDR")

    raw = zlib.decompress(idat)
    channels: int = 3 if colour == 2 else 4
    bpp = channels
    stride = width * bpp
    rows, previous, cursor = [], bytearray(stride), 0
    for _ in range(height):
        filter_type = raw[cursor]
        cursor += 1
        line = bytearray(raw[cursor : cursor + stride])
        cursor += stride
        if filter_type == 1:
            for i in range(bpp, stride):
                line[i] = (line[i] + line[i - bpp]) & 255
        elif filter_type == 2:
            for i in range(stride):
                line[i] = (line[i] + previous[i]) & 255
        elif filter_type == 3:
            for i in range(stride):
                left = line[i - bpp] if i >= bpp else 0
                line[i] = (line[i] + ((left + previous[i]) >> 1)) & 255
        elif filter_type == 4:
            for i in range(stride):
                left = line[i - bpp] if i >= bpp else 0
                up = previous[i]
                upleft = previous[i - bpp] if i >= bpp else 0
                pa, pb, pc = abs(up - upleft), abs(left - upleft), abs(left + up - 2 * upleft)
                pred = left if (pa <= pb and pa <= pc) else (up if pb <= pc else upleft)
                line[i] = (line[i] + pred) & 255
        elif filter_type != 0:
            raise ValueError(f"{path}: unknown filter {filter_type}")
        rows.append(line)
        previous = line
    return width, height, bpp, rows


def content_box(path):
    width, height, bpp, rows = read_png(path)
    bg = rows[0][:bpp]
    min_x, min_y, max_x, max_y = width, height, -1, -1
    for y, line in enumerate(rows):
        for x in range(width):
            offset = x * bpp
            if (
                abs(line[offset] - bg[0]) > TOLERANCE
                or abs(line[offset + 1] - bg[1]) > TOLERANCE
                or abs(line[offset + 2] - bg[2]) > TOLERANCE
            ):
                if x < min_x:
                    min_x = x
                if x > max_x:
                    max_x = x
                if y < min_y:
                    min_y = y
                if y > max_y:
                    max_y = y
    if max_x < 0:
        return None
    return width, height, min_x, min_y, max_x, max_y


def crop_box(path):
    found = content_box(path)
    if not found:
        return None, None
    width, height, min_x, min_y, max_x, max_y = found
    content_w, content_h = max_x - min_x + 1, max_y - min_y + 1
    side = int(max(content_w, content_h) * (1 + 2 * PADDING))
    side = min(side, width, height)              # never leave the image
    centre_x = (min_x + max_x) // 2
    centre_y = (min_y + max_y) // 2
    left = max(0, min(width - side, centre_x - side // 2))
    top = max(0, min(height - side, centre_y - side // 2))
    return (side, left, top), (content_w, content_h, width, height)


def main():
    targets = sys.argv[1:] or sorted(glob.glob("public/mock/*.png"))
    for path in targets:
        box, info = crop_box(path)
        if box is None or info is None:
            print(f"· {os.path.basename(path)}: no content found, left alone")
            continue
        side, left, top = box
        content_w, content_h, width, height = info
        tmp = f"{path}.crop.png"
        subprocess.run(
            [FFMPEG, "-y", "-v", "error", "-i", path,
             "-vf", f"crop={side}:{side}:{left}:{top},scale={SIZE}:{SIZE}:flags=lanczos",
             "-pix_fmt", "rgb24", tmp],
            check=True,
        )
        os.replace(tmp, path)
        share = round(100 * max(content_w, content_h) / side)
        print(
            f"✓ {os.path.basename(path):<28} content {content_w}x{content_h} of {width}x{height} "
            f"→ crop {side}² at ({left},{top}); mark now fills {share}% of the box"
        )


if __name__ == "__main__":
    main()
