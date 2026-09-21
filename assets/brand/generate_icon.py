"""Generates the dotsync mark and app icon. Run: python3 assets/brand/generate_icon.py

The mark is a dot (the "dot" in dotfiles) inside two arcs turning around it (sync).
"""
import math
import sys

C, R = 32.0, 15.5          # circle centre and radius on a 64×64 grid
STROKE, DOT = 5.0, 5.75     # arc stroke width, centre dot radius
GAP = 38                    # degrees left open between the two arcs
HEAD = 5.6                  # arrowhead arm length


def pt(a):
    return C + R * math.cos(math.radians(a)), C + R * math.sin(math.radians(a))


def arc(a0, a1):
    """Arc drawn counter-clockwise on screen from angle a0 down to a1 (a0 > a1)."""
    (x0, y0), (x1, y1) = pt(a0), pt(a1)
    return f"M{x0:.2f} {y0:.2f}A{R} {R} 0 0 0 {x1:.2f} {y1:.2f}"


def head(a):
    """Chevron at the end of an arc ending at angle a, aligned with the curve (chord direction)."""
    x, y = pt(a)
    back = math.degrees(HEAD / R)             # step back along the arc by the head length
    bx, by = pt(a + back)
    dx, dy = x - bx, y - by
    n = math.hypot(dx, dy)
    dx, dy = dx / n, dy / n
    px, py = -dy, dx
    w = HEAD * 0.78
    p1 = (x - dx * HEAD + px * w, y - dy * HEAD + py * w)
    p2 = (x - dx * HEAD - px * w, y - dy * HEAD - py * w)
    return f"M{p1[0]:.2f} {p1[1]:.2f}L{x:.2f} {y:.2f}L{p2[0]:.2f} {p2[1]:.2f}"


def paths(arrows=True):
    """Two arcs, each turning counter-clockwise, with gaps at 1 and 7 o'clock."""
    h = GAP / 2
    ends = [(-20 - h, -200 + h), (160 - h, -20 + h)]   # (start, end) angles of each arc
    out = [arc(a0, a1) for a0, a1 in ends]
    if arrows:
        out += [head(a1) for _, a1 in ends]
    return "".join(f'<path d="{d}"/>' for d in out)


def mark(stroke, dot, arrows=True):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none">'
            f'<g stroke="{stroke}" stroke-width="{STROKE}" stroke-linecap="round" stroke-linejoin="round">{paths(arrows)}</g>'
            f'<circle cx="32" cy="32" r="{DOT}" fill="{dot}"/></svg>\n')


def icon(arrows=True):
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none">
  <title>dotsync</title>
  <defs>
    <linearGradient id="bg" x1="8" y1="4" x2="56" y2="60" gradientUnits="userSpaceOnUse">
      <stop offset="0" stop-color="#2dd4bf"/>
      <stop offset="1" stop-color="#0f766e"/>
    </linearGradient>
  </defs>
  <rect width="64" height="64" rx="15" fill="url(#bg)"/>
  <g stroke="#ffffff" stroke-width="{STROKE}" stroke-linecap="round" stroke-linejoin="round">{paths(arrows)}</g>
  <circle cx="32" cy="32" r="{DOT}" fill="#ffffff"/>
</svg>
'''


if __name__ == "__main__":
    out = sys.argv[1] if len(sys.argv) > 1 else "assets/brand"
    arrows = "--no-arrows" not in sys.argv
    files = {
        "icon.svg": icon(arrows),
        "mark.svg": mark("currentColor", "currentColor", arrows),
        "mark-dark.svg": mark("#2dd4bf", "#e8edf2", arrows),   # on dark backgrounds
        "mark-light.svg": mark("#0f766e", "#0f1520", arrows),  # on light backgrounds
    }
    for name, svg in files.items():
        with open(f"{out}/{name}", "w") as f:
            f.write(svg)
