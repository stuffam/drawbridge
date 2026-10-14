"""Draws stack.svg, the diagram of Drawbridge's pieces and paths that the README shows.

The SVG is made by this script and nothing else: change the drawing here, run `make diagrams`, and
commit both files. test/docs/test_diagrams.py fails when the SVG isn't what this script draws, and
when any line of text is likely to be wider than its box.

Every fact in it comes from the code as built, not from docs/PLAN.md (which describes the target):
the units in packaging/systemd/, the files in packaging/nfpm.yaml, the defaults in cmd/drawbridge/
and internal/model/, and CLAUDE.md. When one of those changes, change the drawing with it, and
AS_OF with the release it shows.

Text in an SVG doesn't wrap, so every line is placed by hand. `est` guesses a line's width
cautiously (wider than macOS's fonts draw it), so a line that passes should fit on other systems'
fonts too. It uses only the standard library.

    python3 docs/diagrams/stack.py [OUT.svg]
"""

import sys
from pathlib import Path
from xml.sax.saxutils import escape

AS_OF = "v0.1.0"
W, H = 1720, 1256
SANS = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"
MONO = "ui-monospace, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace"

INK, MUTED, CAPS = "#111827", "#4B5563", "#374151"
BG, CARD, LINE = "#F5F6F8", "#FFFFFF", "#C7CFDA"
GREEN, BLUE, PURPLE, AMBER, SLATE = "#047857", "#1D4ED8", "#6D28D9", "#B45309", "#475569"
TINT_USER, TINT_STATE, TINT_KERNEL = "#EDF2FE", "#FDF4E3", "#E6F5EE"

out, warnings = [], []


def n(v):
    """A number as the SVG writes it: no float noise, so the output is the same everywhere."""
    if isinstance(v, float):
        return f"{v:.1f}".rstrip("0").rstrip(".")
    return str(v)


def est(text, size, mono=False, bold=False):
    """A cautious estimate of a line's width in pixels."""
    return len(text) * size * (0.61 if mono else (0.57 if bold else 0.54))


def fits(text, size, width, mono=False, bold=False, where=""):
    w = est(text, size, mono, bold)
    if w > width:
        warnings.append(f"{where}: {text!r} is about {w:.0f}px, and the box has {width:.0f}px")


def add(s):
    out.append(s)


def text(x, y, s, size=12, fill=INK, mono=False, bold=False, anchor="start", spacing=None,
         keep_spaces=False):
    extra = ' font-weight="650"' if bold else ""
    if spacing:
        extra += f' letter-spacing="{spacing}"'
    if anchor != "start":
        extra += f' text-anchor="{anchor}"'
    if keep_spaces:
        extra += ' xml:space="preserve"'
    add(f'<text x="{n(x)}" y="{n(y)}" font-family="{MONO if mono else SANS}" font-size="{n(size)}" '
        f'fill="{fill}"{extra}>{escape(s)}</text>')


def rect(x, y, w, h, fill=CARD, stroke=LINE, rx=8, sw=1, dash=None):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    add(f'<rect x="{n(x)}" y="{n(y)}" width="{n(w)}" height="{n(h)}" rx="{rx}" fill="{fill}" '
        f'stroke="{stroke}" stroke-width="{sw}"{d}/>')


def card(x, y, w, h, title, lines, title_mono=False, accent=None, fill=CARD, dash=None,
         title_size=13, line_size=12, step=17, pad=12):
    """A box with a title and lines under it. A line that starts with ` is set in monospace."""
    rect(x, y, w, h, fill=fill, dash=dash)
    if accent:
        add(f'<path d="M{x + 8},{y} h{w - 16}" stroke="{accent}" stroke-width="3" '
            f'stroke-linecap="round"/>')
    inner = w - 2 * pad
    fits(title, title_size, inner, mono=title_mono, bold=not title_mono, where=title)
    text(x + pad, y + 24, title, size=title_size, mono=title_mono, bold=True)
    yy = y + 24 + step + 3
    for ln in lines:
        mono = ln.startswith("`")
        s = ln[1:] if mono else ln
        size = line_size - 0.5 if mono else line_size
        fits(s, size, inner, mono=mono, where=title)
        text(x + pad, yy, s, size=size, fill=INK if mono else MUTED, mono=mono)
        yy += step
    if yy - step + 6 > y + h:
        warnings.append(f"{title}: its text runs past the bottom of its box")


def arrow(points, color, width=2, dash=None, both=False):
    d = "M" + " L".join(f"{n(x)},{n(y)}" for x, y in points)
    extra = f' stroke-dasharray="{dash}"' if dash else ""
    start = f' marker-start="url(#s-{color[1:]})"' if both else ""
    add(f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{width}" '
        f'stroke-linejoin="round" stroke-linecap="round" marker-end="url(#a-{color[1:]})"'
        f"{start}{extra}/>")


def label(x, y, s, color, anchor="start", size=11):
    """A path's label. Each one sits in clear space, so nothing goes behind it."""
    fits(s, size, 200, bold=True, where="a path's label")
    text(x, y, s, size=size, fill=color, anchor=anchor, bold=True)


def caps(x, y, s, anchor="start"):
    text(x, y, s, size=10.5, fill=CAPS, bold=True, spacing="0.09em", anchor=anchor)


def draw():
    # ---- the frame ----
    add(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" '
        'role="img" aria-labelledby="t d">')
    add('<title id="t">Drawbridge: the pieces and the paths</title>')
    add('<desc id="d">What runs on a Drawbridge host, who talks to it and over what, where its '
        "files are, and what the package installs.</desc>")
    add("<defs>")
    for c in (GREEN, BLUE, PURPLE, AMBER, SLATE):
        for kind in ("a", "s"):  # the arrowhead at a path's end, and at its start
            add(f'<marker id="{kind}-{c[1:]}" viewBox="0 0 10 10" refX="8.5" refY="5" '
                'markerWidth="7" markerHeight="7" orient="auto-start-reverse">'
                f'<path d="M0,0.8 L9,5 L0,9.2 Z" fill="{c}"/></marker>')
    add("</defs>")
    add(f'<rect width="{W}" height="{H}" fill="{BG}"/>')

    # ---- the title and the legend ----
    text(40, 52, "Drawbridge: the pieces and the paths", size=27, bold=True)
    text(40, 78, "What runs on the host, who talks to it, and where everything lives. "
         f"As built in {AS_OF}.", size=13.5, fill=MUTED)
    legend = [
        (GREEN, None, "VPN traffic"),
        (BLUE, None, "Web UI and API, over HTTPS"),
        (PURPLE, None, "Command line, over a Unix socket"),
        (AMBER, None, "Changes the kernel's state"),
        (SLATE, None, "Reads or writes a file"),
        (SLATE, "5 4", "Polls, or is optional"),
    ]
    for i, (c, dash, s) in enumerate(legend):
        lx, ly = 960 + (i % 3) * 248, 44 + (i // 3) * 24
        d = f' stroke-dasharray="{dash}"' if dash else ""
        add(f'<path d="M{lx},{ly - 4} h30" stroke="{c}" stroke-width="2.5" '
            f'stroke-linecap="round"{d}/>')
        fits(s, 11.5, 200, where="the legend")
        text(lx + 38, ly, s, size=11.5, fill=MUTED)

    # ---- who talks to the host ----
    TOP, TOPH = 112, 92
    card(40, TOP, 220, TOPH, "VPN clients",
         ["Phones and laptops with the", "WireGuard app, from anywhere."], accent=GREEN)
    card(500, TOP, 240, TOPH, "The admin's browser",
         ["On the home network, or", "through the VPN."], accent=BLUE)
    card(780, TOP, 244, TOPH, "A dashboard, such as Homepage",
         ["A read-only API token.", "GET routes only."], accent=BLUE, title_size=12.5)
    card(1210, TOP, 220, TOPH, "The admin's terminal",
         ["On the host, over SSH:", "`sudo drawbridge …"], accent=PURPLE)
    card(1460, TOP, 220, TOPH, "The internet",
         ["And your home network: where", "clients' traffic comes out."], accent=GREEN)

    # ---- the host ----
    rect(40, 262, 1640, 790, fill="#FFFFFF", stroke="#1F2937", rx=14, sw=1.5)
    caps(62, 286, "THE HOST  ·  DEBIAN-FAMILY LINUX WITH SYSTEMD, ARM64 OR AMD64")
    LX, LW = 60, 1600  # the three layers inside it

    # Drawbridge, in user space
    rect(LX, 298, LW, 302, fill=TINT_USER, stroke="none", rx=10)
    caps(80, 320, "DRAWBRIDGE  ·  ONE BINARY  ·  USER drawbridge  ·  CAP_NET_ADMIN ONLY")
    BY, BH = 336, 244  # the layer's boxes
    BB = BY + BH       # and their bottom

    card(80, BY, 240, BH, "drawbridge-tunnel.service", [
        "`drawbridge tunnel up | down", "",
        "Runs once at boot. Brings the VPN", "up from the database, using the",
        "daemon's own reconciler.", "",
        "The VPN keeps running when the", "daemon stops or is upgraded.", "",
        "Stopping this unit is what takes", "the VPN down.",
    ], title_mono=True, title_size=12.5)

    DX, DW = 372, 808  # the daemon
    rect(DX, BY, DW, BH)
    text(DX + 16, BY + 24, "drawbridge.service", size=12.5, mono=True, bold=True)
    text(DX + 16 + est("drawbridge.service", 12.5, mono=True) + 10, BY + 24,
         "runs  drawbridge serve: the daemon, with the web UI inside it", size=12, fill=MUTED)
    CW, CH, CG = 250, 84, 16
    cx = [DX + 16, DX + 16 + CW + CG, DX + 16 + 2 * (CW + CG)]
    cw3 = DX + DW - 16 - cx[2]  # the third column takes what's left
    r1, r2 = BY + 40, BY + 40 + CH + 20
    chip = dict(fill="#F8FAFC", title_size=12.5, line_size=11.5, step=16, pad=10)
    card(cx[0], r1, CW, CH, "Web UI and API", [
        "HTTPS on TCP 51821: the web app,", "/api/, and a live event stream.",
        "Home network and VPN only."], **chip)
    card(cx[1], r1, CW, CH, "Every change, in one order", [
        "validate → database →", "apply to the kernel → event.",
        "The same for the web and the CLI."], **chip)
    card(cx[2], r1, cw3, CH, "Control socket", [
        "`/run/drawbridge/control.sock", "A Unix socket, for root and the",
        "drawbridge group."], **chip)
    card(cx[0], r2, CW, CH, "Monitor", [
        "Polls wg0 every 5 s: who is", "connected, sessions, traffic.",
        "Writes to disk once a minute."], **chip)
    card(cx[1], r2, CW, CH, "Reconciler", [
        "Makes the kernel match the", "database: after each change,",
        "and every 30 s to undo drift."], **chip)
    card(cx[2], r2, cw3, CH, "Doctor and integrations", [
        "13 checks of the host and network.", "AdGuard Home: name sync and",
        "a client's DNS log."], **chip)
    CHIPB = r2 + CH  # the chips' bottom

    QX, QW = 1200, 440  # the command line, and AdGuard Home under it
    rect(QX, BY, QW, 158)
    text(QX + 12, BY + 24, "The command line", size=13, bold=True)
    text(QX + 12 + est("The command line", 13, bold=True) + 8, BY + 24, "drawbridge …",
         size=12, mono=True, fill=MUTED)
    cli = [
        "client  list · add · show · pause · resume · rename · delete",
        "        config · qr · rotate-keys",
        "server  show · set · rotate-key · confirm · revert",
        "admin   setup-token · create · reset-password · disable-2fa",
        "backup  create · restore        tls  show · install · reset",
        "also    events · doctor · apply · version",
    ]
    for i, ln in enumerate(cli):
        fits(ln, 11, QW - 24, mono=True, where="the command line")
        text(QX + 12, BY + 46 + i * 16, ln, size=11, mono=True, keep_spaces=True)
    note = "Through the control socket. Only backup restore opens the database."
    fits(note, 11, QW - 24, where="the command line")
    text(QX + 12, BY + 148, note, size=11, fill=MUTED)
    AY = BY + 170
    card(QX, AY, QW, BB - AY, "AdGuard Home, if you run it", [
        "`http://127.0.0.1:3000/control", "Optional. The VPN works the same without it."],
        dash="5 4", title_size=12.5, line_size=11.5, step=16)

    # state on disk
    rect(LX, 624, LW, 176, fill=TINT_STATE, stroke="none", rx=10)
    caps(80, 646, "STATE ON DISK")
    CY, CHH = 660, 122
    st = dict(title_mono=True, title_size=11.5, line_size=11.5, step=16)
    card(80, CY, 228, CHH, "/etc/drawbridge/secret.key", [
        "32 bytes, made at install.", "Unseals the private keys in the",
        "database. A backup has it;", "a snapshot doesn't."], **st)
    card(336, CY, 280, CHH, "/var/lib/drawbridge/drawbridge.db", [
        "SQLite, and the source of truth:", "settings, clients, sealed keys, events,",
        "sessions, and traffic history.", "Mode 0600."], **st)
    card(644, CY, 228, CHH, "/var/lib/drawbridge/tls/", [
        "cert.pem and key.pem: the", "self-signed certificate.", "uploaded.pem: your own."], **st)
    card(900, CY, 228, CHH, "/var/lib/drawbridge/backups/", [
        "Snapshots of the database:", "one a night, and one before", "each migration."], **st)
    card(1156, CY, 258, CHH, "/var/lib/drawbridge/nftables.conf", [
        "The last ruleset applied, kept", "for you to read."], **st)
    card(1442, CY, 198, CHH, "The systemd journal", [
        "Every event, with fields", "such as DRAWBRIDGE_EVENT", "and DRAWBRIDGE_CLIENT."],
        title_size=12.5, line_size=11.5, step=16)

    # the kernel
    rect(LX, 816, LW, 218, fill=TINT_KERNEL, stroke="none", rx=10)
    caps(1640, 838, "THE KERNEL  ·  DRAWBRIDGE CHANGES ONLY ITS OWN INTERFACE AND ITS OWN TABLE",
         anchor="end")
    KC, KCH = 878, 136
    kn = dict(title_size=13, line_size=11.5, step=17)
    card(80, KC, 340, KCH, "wg0, the WireGuard interface", [
        "Listens on UDP 51820. One peer for each client,", "with only the addresses it may use.",
        "By default 10.8.0.0/24 and a private IPv6 /64."], **kn)
    card(460, KC, 260, KCH, "IP forwarding", [
        "`net.ipv4.ip_forward", "`net.ipv6.conf.all.forwarding", "Turned on by the package."], **kn)
    card(760, KC, 480, KCH, "nftables: table inet drawbridge", [
        "Lets clients' traffic through, and sends it out under the host's own",
        "address (NAT44 and NAT66).",
        "Drops TCP 51821 from anywhere but the home network and the VPN.",
        "Drawbridge never touches another table."], **kn)
    card(1280, KC, 360, KCH, "The uplink (eth0, wlan0, …)", [
        "The interface with the default route. Its subnets",
        "are what Drawbridge counts as the home network."], **kn)

    # ---- what the package installs ----
    PY = 1070
    rect(40, PY, 1640, 166, fill="#FFFFFF", stroke=LINE, rx=12)
    caps(62, PY + 26, "PUT THERE BY THE PACKAGE  ·  drawbridge_<version>_<arch>.deb, FROM A GITHUB "
         "RELEASE, BY HAND OR WITH install.sh")
    rows = [
        [("/usr/bin/drawbridge", "The program: daemon, tunnel, and CLI, with the web UI inside"),
         ("/usr/lib/systemd/system/drawbridge.service", "The daemon's unit"),
         ("/usr/lib/systemd/system/drawbridge-tunnel.service", "The tunnel's unit"),
         ("/usr/lib/sysusers.d/drawbridge.conf", "Makes the drawbridge user"),
         ("/usr/share/doc/drawbridge/copyright", "The license")],
        [("/usr/lib/sysctl.d/90-drawbridge.conf", "Turns on IPv4 and IPv6 forwarding"),
         ("/usr/lib/modules-load.d/drawbridge.conf", "Loads the wireguard module at boot"),
         ("/usr/lib/NetworkManager/conf.d/90-drawbridge.conf",
          "Tells NetworkManager to leave wg0 alone"),
         ("/usr/lib/drawbridge/accept-ra", "Run at install, to keep the host's own IPv6"),
         ("/etc/sysctl.d/91-drawbridge-accept-ra.conf", "Written only where that was needed")],
    ]
    for col, items in enumerate(rows):
        px = 62 + col * 820
        for i, (path, what) in enumerate(items):
            yy = PY + 54 + i * 22
            fits(path, 11.5, 366, mono=True, where="the package")
            fits(what, 11.5, 420, where="the package")
            text(px, yy, path, size=11.5, mono=True)
            text(px + 376, yy, what, size=11.5, fill=MUTED)

    # ---- the paths ----
    # VPN traffic: the clients, the router, wg0, forwarding, the table, the uplink, and out
    arrow([(50, TOP + TOPH), (50, 960), (80, 960)], GREEN, 2.5)
    add(f'<circle cx="50" cy="234" r="4" fill="{GREEN}"/>')
    add(f'<rect x="62" y="222" width="212" height="24" rx="12" fill="#FFFFFF" stroke="{GREEN}" '
        'stroke-width="1"/>')
    fits("Home router forwards UDP 51820", 11, 196, bold=True, where="the router")
    text(74, 238, "Home router forwards UDP 51820", size=11, fill=GREEN, bold=True)
    KM = KC + 68
    arrow([(420, KM), (460, KM)], GREEN, 2.5)
    arrow([(720, KM), (760, KM)], GREEN, 2.5)
    arrow([(1240, KM), (1280, KM)], GREEN, 2.5)
    arrow([(1640, KM), (1670, KM), (1670, TOP + TOPH)], GREEN, 2.5)
    label(1660, 246, "Out the uplink, after NAT", GREEN, anchor="end")

    # the web UI and the API
    arrow([(620, TOP + TOPH), (620, BY)], BLUE)
    label(612, 250, "HTTPS, TCP 51821", BLUE, anchor="end")
    arrow([(800, TOP + TOPH), (800, 232), (640, 232), (640, BY)], BLUE)
    label(650, 250, "Same port, with the token", BLUE)

    # the command line
    arrow([(1320, TOP + TOPH), (1320, BY)], PURPLE)
    label(1328, 246, "Runs commands", PURPLE)
    arrow([(QX, r1 + 42), (cx[2] + cw3, r1 + 42)], PURPLE)

    # the reconciler and the tunnel unit change the kernel's state
    arrow([(886, CHIPB), (886, KC)], AMBER)
    arrow([(886, 860), (300, 860), (300, KC)], AMBER)
    label(896, 616, "wgctrl, netlink, nft -f", AMBER)
    arrow([(92, BB), (92, 612), (70, 612), (70, 902), (80, 902)], AMBER)

    # the monitor polls wg0
    arrow([(630, CHIPB), (630, 848), (200, 848), (200, KC)], SLATE, dash="5 4")
    label(640, 616, "Polls the peers", SLATE)

    # the files
    arrow([(250, BB), (250, 594), (400, 594), (400, CY)], SLATE)
    label(408, 616, "Reads", SLATE)
    arrow([(480, BB), (480, CY)], SLATE, both=True)
    label(488, 616, "Reads and writes", SLATE)
    arrow([(308, CY + 60), (336, CY + 60)], SLATE)
    arrow([(760, BB), (760, CY)], SLATE, both=True)
    label(768, 616, "Certificates", SLATE)
    arrow([(1040, BB), (1040, CY)], SLATE)
    label(1048, 616, "Snapshots", SLATE)
    arrow([(1168, BB), (1168, CY)], SLATE)
    label(1176, 616, "Saves the ruleset", SLATE)

    # AdGuard Home is optional
    arrow([(cx[2] + cw3, r2 + 42), (QX, r2 + 42)], SLATE, dash="5 4")

    add("</svg>")
    return "\n".join(out) + "\n"


def main():
    dest = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).with_suffix(".svg")
    svg = draw()
    if warnings:
        for w in warnings:
            print(f"stack.py: {w}", file=sys.stderr)
        print("stack.py: nothing written; make the text fit first", file=sys.stderr)
        return 1
    dest.write_text(svg, encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
