#!/usr/bin/env python3
"""Generates the README images in docs/assets: python3 scripts/readme-images.py docs/assets"""
import os, sys
OUT = sys.argv[1]
ART = [
 " ██████╗ ███████╗██╗   ██╗██████╗  ██████╗ ██╗   ██╗████████╗███████╗",
 " ██╔══██╗██╔════╝╚██╗ ██╔╝██╔══██╗██╔═══██╗██║   ██║╚══██╔══╝██╔════╝",
 " ██║  ██║█████╗   ╚████╔╝ ██████╔╝██║   ██║██║   ██║   ██║   █████╗",
 " ██║  ██║██╔══╝    ╚██╔╝  ██╔══██╗██║   ██║██║   ██║   ██║   ██╔══╝",
 " ██████╔╝███████╗   ██║   ██║  ██║╚██████╔╝╚██████╔╝   ██║   ███████╗",
 " ╚═════╝ ╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝   ╚══════╝",
]

def logo():
    cw, ch = 12, 22            # one terminal cell
    cols = max(len(r) for r in ART) - 1
    rows = 5                   # the last row is only shadow
    pad = 6
    w, h = cols * cw + 2 * pad + 8, rows * ch + 2 * pad + 8
    cells = []
    for y, row in enumerate(ART[:rows]):
        x = 0
        for c in row[1:]:
            if c == "█":
                cells.append((x, y))
            x += 1
    # merge horizontal runs into one rect per run
    runs = []
    for y in range(rows):
        xs = sorted(x for x, yy in cells if yy == y)
        start = prev = None
        for x in xs + [None]:
            if start is None:
                start = prev = x
            elif x == prev + 1:
                prev = x
            else:
                runs.append((start, y, prev - start + 1))
                start = prev = x
    def rects(dx, dy):
        return "".join(f'<rect x="{pad + dx + s * cw}" y="{pad + dy + y * ch}" width="{n * cw}" height="{ch}"/>' for s, y, n in runs)
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="DEYROUTE">
  <title>DEYROUTE</title>
  <defs>
    <linearGradient id="g" x1="0" y1="0" x2="1" y2="0.35">
      <stop offset="0" stop-color="#22d3ee"/>
      <stop offset="0.55" stop-color="#3b82f6"/>
      <stop offset="1" stop-color="#8b5cf6"/>
    </linearGradient>
  </defs>
  <g fill="#64748b" fill-opacity="0.45">{rects(6, 6)}</g>
  <g fill="url(#g)">{rects(0, 0)}</g>
</svg>
'''

THEMES = {
    "dark": dict(text="#e6edf3", sub="#9da7b3", box="#161b22", stroke="#30363d", accent="#58a6ff", tunnel1="#22d3ee", tunnel2="#8b5cf6", dash="#7d8590"),
    "light": dict(text="#1f2328", sub="#59636e", box="#f6f8fa", stroke="#d1d9e0", accent="#0969da", tunnel1="#0891b2", tunnel2="#7c3aed", dash="#818b98"),
}
LAT = "ui-sans-serif, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Noto Sans', Helvetica, Arial, sans-serif"
FA = "Vazirmatn, 'Segoe UI', Tahoma, 'Noto Sans Arabic', 'Noto Naskh Arabic', 'Geeza Pro', Arial, sans-serif"
MONO = "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace"

L = {
 "en": dict(users="Your users", users2="phone, laptop, any VPN app",
            to_hub="connects to", hub="HUB", hub_where="in Iran", hub2="the only address users know",
            tunnel="the tunnel", tunnel2="8 methods, switches by itself",
            node="NODE", node_where="abroad", node2="your VPN service runs here",
            backup="Backup node", backup2="takes over if the node fails", font=LAT, rtl=False),
 "fa": dict(users="کاربران شما", users2="گوشی، لپ‌تاپ، هر برنامهٔ VPN",
            to_hub="وصل می‌شوند به", hub="هاب", hub_where="در ایران", hub2="تنها آدرسی که کاربران می‌بینند",
            tunnel="تانل", tunnel2="۸ روش، تعویض خودکار",
            node="نود", node_where="در خارج", node2="سرویس VPN شما اینجاست",
            backup="نود پشتیبان", backup2="اگر نود از کار بیفتد جایش را می‌گیرد", font=FA, rtl=True),
}

def diagram(lang, theme):
    t, s = THEMES[theme], L[lang]
    W, H = 500, 548
    rtl = s["rtl"]
    d = ' direction="rtl"' if rtl else ""
    def text(x, y, txt, size, color, weight=400, font=None, anchor="middle", is_rtl=None):
        f = font or s["font"]
        r = rtl if is_rtl is None else is_rtl
        dd = ' direction="rtl"' if r else ""
        return f'<text x="{x}" y="{y}" font-family="{f}" font-size="{size}" font-weight="{weight}" fill="{color}" text-anchor="{anchor}"{dd}>{txt}</text>'
    def box(x, y, w, h, dashed=False):
        da = ' stroke-dasharray="7 6"' if dashed else ""
        return f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="14" fill="{t["box"]}" stroke="{t["stroke"]}" stroke-width="1.5"{da}/>'
    def chip(x, y, label, sub):
        return text(x, y, f'<tspan font-weight="700" fill="{t["text"]}">{label}</tspan><tspan fill="{t["sub"]}"> · {sub}</tspan>', 22, t["text"])
    def marker(id, color, size):
        return (f'<marker id="{id}" viewBox="0 0 10 10" refX="7" refY="5" markerUnits="userSpaceOnUse" '
                f'markerWidth="{size}" markerHeight="{size}" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 z" fill="{color}"/></marker>')
    p = []
    p.append(f'<defs><linearGradient id="tg" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{t["tunnel1"]}"/>'
             f'<stop offset="1" stop-color="{t["tunnel2"]}"/></linearGradient>'
             + marker("ah", t["accent"], 12) + marker("at", t["tunnel2"], 18) + marker("ad", t["dash"], 12) + '</defs>')
    # users
    p.append(box(100, 16, 300, 84))
    p.append(text(250, 52, s["users"], 22, t["text"], 700))
    p.append(text(250, 80, s["users2"], 15, t["sub"]))
    # users -> hub, label beside the arrow (right of it in English, left of it in Persian)
    p.append(f'<line x1="250" y1="102" x2="250" y2="160" stroke="{t["accent"]}" stroke-width="2.5" marker-end="url(#ah)"/>')
    if rtl:
        p.append(text(238, 126, s["to_hub"], 14, t["sub"], anchor="start"))
        p.append(text(238, 147, "HUB_IP:443", 14, t["accent"], 600, MONO, anchor="end", is_rtl=False))
    else:
        p.append(text(262, 126, s["to_hub"], 14, t["sub"], anchor="start"))
        p.append(text(262, 147, "HUB_IP:443", 14, t["accent"], 600, MONO, anchor="start"))
    # hub
    p.append(box(100, 166, 300, 104))
    p.append(chip(250, 202, s["hub"], s["hub_where"]))
    p.append(text(250, 230, "deyroute hub", 16, t["accent"], 600, MONO, is_rtl=False))
    p.append(text(250, 254, s["hub2"], 14, t["sub"]))
    # tunnel to the node (thick) and the dashed path to the backup node
    p.append(f'<path d="M190 272 C 190 312, 128 312, 128 352" fill="none" stroke="url(#tg)" stroke-width="7" stroke-linecap="round" marker-end="url(#at)"/>')
    p.append(f'<path d="M310 272 C 310 312, 372 312, 372 352" fill="none" stroke="{t["dash"]}" stroke-width="2" stroke-dasharray="6 6" marker-end="url(#ad)"/>')
    p.append(text(250, 306, s["tunnel"], 17, t["text"], 700))
    p.append(text(250, 326, s["tunnel2"], 13, t["sub"]))
    # node
    p.append(box(8, 360, 240, 124))
    p.append(chip(128, 396, s["node"], s["node_where"]))
    p.append(text(128, 424, "deyroute node", 15, t["accent"], 600, MONO, is_rtl=False))
    p.append(text(128, 448, s["node2"], 14, t["sub"]))
    p.append(text(128, 470, "Xray · Marzban · 3x-ui", 13, t["sub"], 400, LAT, is_rtl=False))
    # backup
    p.append(box(256, 360, 236, 124, dashed=True))
    p.append(text(374, 404, s["backup"], 19, t["text"], 700))
    p.append(text(374, 434, s["backup2"], 13, t["sub"]))
    p.append(text(374, 460, "deyroute node", 14, t["dash"], 600, MONO, is_rtl=False))
    note = {"en": "Carried byte for byte: your TLS, SNI and UUID stay unchanged.",
            "fa": "بایت‌به‌بایت حمل می‌شود؛ TLS و SNI و UUID شما دست نمی‌خورد."}[lang]
    p.append(text(250, 524, note, 13, t["sub"]))
    title = {"en": "How DEYROUTE works", "fa": "DEYROUTE چطور کار می‌کند"}[lang]
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="{title}">\n'
            f'  <title>{title}</title>\n  ' + "\n  ".join(p) + "\n</svg>\n")

os.makedirs(OUT, exist_ok=True)
open(os.path.join(OUT, "logo.svg"), "w").write(logo())
for lang in ("en", "fa"):
    for theme in ("dark", "light"):
        open(os.path.join(OUT, f"how-it-works-{lang}-{theme}.svg"), "w").write(diagram(lang, theme))
print(sorted(os.listdir(OUT)))
