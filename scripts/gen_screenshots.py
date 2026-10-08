#!/usr/bin/env python3
"""Regenerate docs/screenshots/*.png from real runs of the CLI.

Every image is produced by executing the exact command shown in it and
capturing its real stdout+stderr and exit status; nothing is typed by hand.
Each capture is also saved as a .txt transcript next to the image.

Runs against a local demo server (127.0.0.1:8765) using the configs in
docs/demo/, so the output is reproducible offline. Every demo response is
delayed by 50ms so the -timeout example times out deterministically.

Requirements: Go, Python 3 (stdlib only), and Chromium's headless shell
(set CHROME=/path/to/headless_shell if it is not found automatically). Full
Chrome's new headless mode shrinks the viewport below --window-size, which
would crop output; every image is checked for that and rejected if cropped.

Usage: python3 scripts/gen_screenshots.py
"""

import html
import os
import shutil
import subprocess
import sys
import tempfile
import struct
import threading
import time
import zlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEMO_DIR = os.path.join(ROOT, "docs", "demo")
OUT_DIR = os.path.join(ROOT, "docs", "screenshots")
HOST, PORT = "127.0.0.1", 8765
DELAY_S = 0.05

# (file stem, window title, command run in docs/demo/ with the binary on PATH)
SHOTS = [
    ("01-sample-run", "sample run", "go-health-checker -config sample.json"),
    ("02-help", "usage", "go-health-checker -h"),
    ("03-timeout-override", "timeout override",
     "go-health-checker -config sample.json -timeout 10ms"),
    ("04-config-error", "missing config",
     "go-health-checker -config does-not-exist.json"),
    ("05-all-healthy", "all endpoints healthy",
     "go-health-checker -config healthy.json"),
]


class DemoHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        time.sleep(DELAY_S)
        if self.path == "/":
            self._reply(200, b"ok\n")
        elif self.path == "/old":
            self.send_response(302)
            self.send_header("Location", "/")
            self.send_header("Content-Length", "0")
            self.end_headers()
        else:
            self._reply(404, b"not found\n")

    def _reply(self, code, body):
        self.send_response(code)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def handle(self):
        # The -timeout example disconnects mid-request; that is expected.
        try:
            super().handle()
        except (BrokenPipeError, ConnectionResetError):
            pass

    def log_message(self, *args):
        pass


def find_chrome():
    candidates = [os.environ.get("CHROME"),
                  "/opt/pw-browsers/chromium_headless_shell-1194/chrome-linux/headless_shell",
                  shutil.which("chrome-headless-shell"), shutil.which("headless_shell")]
    for c in candidates:
        if c and os.path.exists(c):
            return c
    sys.exit("error: no headless shell found; set CHROME=/path/to/headless_shell")


def render(title, command, output, code, png_path, chrome):
    lines = output.rstrip("\n").split("\n") if output else []
    cols = max([len(command) + 2] + [len(line) for line in lines] + [60])
    char_w, line_h, pad = 8.0, 19, 20
    width = int(cols * char_w) + 2 * pad + 20
    height = 36 + pad + line_h * (len(lines) + 1) + pad + 32

    ok = code == 0
    body = "\n".join(html.escape(line) for line in lines)
    page = f"""<!doctype html><html><head><meta charset="utf-8"><style>
html,body{{margin:0;background:#0d1117;}}
.win{{width:{width}px;height:{height}px;box-sizing:border-box;overflow:hidden;
  font:13px/{line_h}px "DejaVu Sans Mono","Menlo",monospace;color:#e6edf3;}}
.bar{{height:36px;display:flex;align-items:center;padding:0 14px;
  background:#161b22;border-bottom:1px solid #30363d;color:#8b949e;font-size:12px;}}
.dot{{width:12px;height:12px;border-radius:50%;margin-right:8px;}}
pre{{margin:0;padding:{pad}px;white-space:pre;font:inherit;}}
.p{{color:#58a6ff;}} .c{{font-weight:bold;}}
.st{{height:32px;line-height:32px;padding:0 {pad}px;border-top:1px solid #30363d;
  background:#161b22;font-size:12px;color:{'#3fb950' if ok else '#f85149'};}}
</style></head><body><div class="win">
<div class="bar"><span class="dot" style="background:#ff5f56"></span>
<span class="dot" style="background:#ffbd2e"></span>
<span class="dot" style="background:#27c93f"></span>
<span style="margin-left:8px">go-health-checker — {html.escape(title)}</span></div>
<pre><span class="p">$</span> <span class="c">{html.escape(command)}</span>
{body}</pre>
<div class="st">exit status {code}</div>
</div></body></html>"""

    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False) as f:
        f.write(page)
        html_path = f.name
    try:
        subprocess.run(
            [chrome, "--headless", "--no-sandbox", "--disable-gpu",
             "--hide-scrollbars", "--force-device-scale-factor=2",
             f"--window-size={width},{height}",
             f"--screenshot={png_path}", "file://" + html_path],
            check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    finally:
        os.unlink(html_path)

    # The status bar is the last element on the page: if the bottom row of
    # the image is not status-bar colour, the screenshot was cropped.
    bottom = png_last_row(png_path)
    if any(px != STATUS_BG for px in (bottom[0], bottom[len(bottom) // 2], bottom[-1])):
        sys.exit(f"error: {png_path} looks cropped (bottom row {bottom[0]}); "
                 "use Chromium's headless_shell")


STATUS_BG = (0x16, 0x1b, 0x22)


def png_last_row(path):
    """Return the RGB pixels of the last row of an 8-bit RGB/RGBA PNG."""
    with open(path, "rb") as f:
        data = f.read()
    pos, idat, width, bpp = 8, b"", 0, 3
    while pos < len(data):
        length, kind = struct.unpack(">I4s", data[pos:pos + 8])
        chunk = data[pos + 8:pos + 8 + length]
        if kind == b"IHDR":
            width, _, depth, color = struct.unpack(">IIBB", chunk[:10])
            assert depth == 8 and color in (2, 6), "unsupported PNG format"
            bpp = 3 if color == 2 else 4
        elif kind == b"IDAT":
            idat += chunk
        pos += 12 + length
    raw, stride = zlib.decompress(idat), width * bpp
    prev = bytearray(stride)
    for i in range(0, len(raw), stride + 1):
        ftype, line = raw[i], bytearray(raw[i + 1:i + 1 + stride])
        for x in range(stride):
            a = line[x - bpp] if x >= bpp else 0
            b, c = prev[x], prev[x - bpp] if x >= bpp else 0
            if ftype == 1:
                line[x] = (line[x] + a) & 0xFF
            elif ftype == 2:
                line[x] = (line[x] + b) & 0xFF
            elif ftype == 3:
                line[x] = (line[x] + (a + b) // 2) & 0xFF
            elif ftype == 4:
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                pred = a if pa <= pb and pa <= pc else (b if pb <= pc else c)
                line[x] = (line[x] + pred) & 0xFF
        prev = line
    return [tuple(prev[x:x + 3]) for x in range(0, stride, bpp)]


def main():
    chrome = find_chrome()
    bindir = tempfile.mkdtemp()
    subprocess.run(["go", "build", "-o", os.path.join(bindir, "go-health-checker"), "."],
                   cwd=ROOT, check=True)

    server = ThreadingHTTPServer((HOST, PORT), DemoHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()

    env = dict(os.environ, PATH=bindir + os.pathsep + os.environ["PATH"])
    try:
        for stem, title, command in SHOTS:
            proc = subprocess.run(["bash", "-c", command], cwd=DEMO_DIR, env=env,
                                  stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                  text=True)
            transcript = f"$ {command}\n{proc.stdout}[exit status {proc.returncode}]\n"
            with open(os.path.join(OUT_DIR, stem + ".txt"), "w") as f:
                f.write(transcript)
            render(title, command, proc.stdout, proc.returncode,
                   os.path.join(OUT_DIR, stem + ".png"), chrome)
            print(f"{stem}: exit {proc.returncode}")
    finally:
        server.shutdown()
        shutil.rmtree(bindir)


if __name__ == "__main__":
    main()
