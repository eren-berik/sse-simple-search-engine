"""Draws the README charts of filters.py's results as SVGs that follow the light/dark theme.

The numbers are from one run (README: Ölçüm: etiket ve tarih filtreleri); update them after a
new run, then:
    python3 scripts/search-benchmark/charts.py docs/images
"""
import sys
from html import escape

OUT = sys.argv[1]

STYLE = """
<style>
  .surface { fill: #fcfcfb; stroke: rgba(11,11,11,0.10); }
  .title { fill: #0b0b0b; font: 600 16px system-ui, -apple-system, "Segoe UI", sans-serif; }
  .sub { fill: #52514e; font: 13px system-ui, -apple-system, "Segoe UI", sans-serif; }
  .label { fill: #0b0b0b; font: 13px system-ui, -apple-system, "Segoe UI", sans-serif; }
  .note { fill: #898781; font: 12px system-ui, -apple-system, "Segoe UI", sans-serif; }
  .value { fill: #0b0b0b; font: 600 12.5px system-ui, -apple-system, "Segoe UI", sans-serif; font-variant-numeric: tabular-nums; }
  .tick { fill: #898781; font: 11.5px system-ui, -apple-system, "Segoe UI", sans-serif; font-variant-numeric: tabular-nums; }
  .grid { stroke: #e1e0d9; stroke-width: 1; }
  .axis { stroke: #c3c2b7; stroke-width: 1; }
  .s1 { fill: #2a78d6; }
  .s2 { fill: #eb6834; }
  @media (prefers-color-scheme: dark) {
    .surface { fill: #1a1a19; stroke: rgba(255,255,255,0.10); }
    .title, .label, .value { fill: #f0efec; }
    .sub { fill: #c3c2b7; }
    .grid { stroke: #2c2c2a; }
    .axis { stroke: #383835; }
    .s1 { fill: #3987e5; }
    .s2 { fill: #d95926; }
  }
</style>"""


def ms(v):
    s = f"{v:.1f}" if v < 10 else f"{v:.0f}"
    return s.replace(".", ",") + " ms"


def bar(x, y, w, h, cls):
    # 4px rounded data end, square at the baseline.
    r = min(4, w / 2)
    return (f'<path class="{cls}" d="M{x},{y} H{x + w - r} Q{x + w},{y} {x + w},{y + r} '
            f'V{y + h - r} Q{x + w},{y + h} {x + w - r},{y + h} H{x} Z"/>')


def frame(width, height, title, sub, desc):
    return [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
            f'viewBox="0 0 {width} {height}" role="img" aria-labelledby="t d">',
            f'<title id="t">{escape(title)}</title><desc id="d">{escape(desc)}</desc>', STYLE,
            f'<rect class="surface" x="0.5" y="0.5" width="{width - 1}" height="{height - 1}" rx="12"/>',
            f'<text class="title" x="24" y="34">{escape(title)}</text>',
            f'<text class="sub" x="24" y="55">{escape(sub)}</text>']


def axis(out, x0, plot_w, top, bottom, vmax, step):
    for v in range(0, vmax + 1, step):
        x = x0 + plot_w * v / vmax
        out.append(f'<line class="{"axis" if v == 0 else "grid"}" x1="{x}" y1="{top}" x2="{x}" y2="{bottom}"/>')
        out.append(f'<text class="tick" x="{x}" y="{bottom + 18}" text-anchor="middle">{v} ms</text>')


def tags_chart():
    rows = [  # label, matching rows, count + page ms
        ("Filtre yok (karşılaştırma)", "750.000", 198.9),
        ("10 yaygın etiketten herhangi biri", "75.450", 109.5),
        ("go, docker, kubernetes'ten herhangi biri", "42.015", 89.3),
        ("Yaygın etiket: go", "24.707", 70.7),
        ("go + tür: metin", "8.277", 29.6),
        ("go ve docker birlikte (hepsi)", "202", 3.2),
        ("Nadir etiket: w4000", "173", 1.3),
    ]
    width, label_w, plot_w, row_h, top = 780, 300, 380, 44, 84
    x0 = 24 + label_w
    bottom = top + row_h * len(rows)
    out = frame(width, bottom + 84, "Etiket filtresi · 1 milyon kayıt",
                "Toplam süre (sayım + 20'lik sayfa). Sayfa her senaryoda en fazla 2 ms; kalan süre toplam sayımı.",
                "Etiket filtresi senaryolarının sorgu süreleri: " +
                "; ".join(f"{l}: {ms(v)}, {n} kayıt" for l, n, v in rows))
    axis(out, x0, plot_w, top - 8, bottom, 200, 50)
    for i, (label, n, v) in enumerate(rows):
        y = top + i * row_h
        out.append(f'<text class="label" x="24" y="{y + 15}">{escape(label)}</text>')
        out.append(f'<text class="note" x="24" y="{y + 31}">{n} kayıt eşleşiyor</text>')
        w = max(plot_w * v / 200, 2)
        out.append(bar(x0, y + 6, w, 20, "s1"))
        out.append(f'<text class="value" x="{x0 + w + 8}" y="{y + 20.5}">{ms(v)}</text>')
    out.append(f'<text class="note" x="24" y="{bottom + 48}">Etiket index\'inin taranması yaygın etiketlerde bile ~3 ms; '
               f'süreyi eşleşen satırların tablodan okunması belirliyor.</text>')
    out.append(f'<text class="note" x="24" y="{bottom + 66}">Süreler cache\'te olmayan istek içindir; sonuçlar Redis\'te 90 sn cache\'lenir.</text>')
    out.append("</svg>")
    return "\n".join(out)


def date_chart():
    groups = [  # label, matching rows, without index, with index
        ("Son 7 gün", "7.138", 144.5, 17.2),
        ("go etiketi + son 7 gün", "231", 172.0, 9.3),
    ]
    width, label_w, plot_w, group_h, top = 780, 220, 440, 76, 108
    x0 = 24 + label_w
    bottom = top + group_h * len(groups)
    out = frame(width, bottom + 48, "Tarih filtresi · published_at index'i",
                "Toplam süre (sayım + 20'lik sayfa), 1 milyon kayıt.",
                "published_at index'i olmadan ve index ile sorgu süreleri: " +
                "; ".join(f"{l}: index yok {ms(a)}, index var {ms(b)}" for l, _, a, b in groups))
    # Legend: two series.
    lx = 24
    for cls, name in (("s2", "Index yok"), ("s1", "Index var")):
        out.append(f'<rect class="{cls}" x="{lx}" y="70" width="12" height="12" rx="3"/>')
        out.append(f'<text class="sub" x="{lx + 18}" y="80.5">{name}</text>')
        lx += 110
    axis(out, x0, plot_w, top - 8, bottom - 8, 200, 50)
    for i, (label, n, before, after) in enumerate(groups):
        y = top + i * group_h
        out.append(f'<text class="label" x="24" y="{y + 20}">{escape(label)}</text>')
        out.append(f'<text class="note" x="24" y="{y + 37}">{n} kayıt eşleşiyor</text>')
        for j, (v, cls) in enumerate(((before, "s2"), (after, "s1"))):
            by = y + 4 + j * 24  # 22px bars with a 2px gap
            w = max(plot_w * v / 200, 2)
            out.append(bar(x0, by, w, 22, cls))
            out.append(f'<text class="value" x="{x0 + w + 8}" y="{by + 15.5}">{ms(v)}</text>')
        speedup = before / after
        out.append(f'<text class="note" x="24" y="{y + 54}">{speedup:.0f} kat hızlı</text>'.replace(".", ","))
    out.append("</svg>")
    return "\n".join(out)


with open(f"{OUT}/bench-tag-filter.svg", "w") as f:
    f.write(tags_chart())
with open(f"{OUT}/bench-date-index.svg", "w") as f:
    f.write(date_chart())
