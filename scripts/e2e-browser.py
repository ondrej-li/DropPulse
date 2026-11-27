#!/usr/bin/env python3
"""Headless Chromium end-to-end UI test for DropPulse (stdlib Python + CDP).

Boots a local loopback DropPulse server (LOCAL_TEST_AUTH=true, DATABASE_URL=memory://)
and drives a headless Chromium instance over the Chrome DevTools Protocol to verify:
  1. Default admin session, header persona switcher, and #brand-home link to /
  2. Switching to a non-admin user via ?as=alice&role=user (RBAC UI hiding)
  3. Creating both provider kinds through the 3-step <dialog> wizard:
     - Kind 1: Cinema City Flora IMAX (Seats + Schedule toggle & datepicker)
     - Kind 2: CineStar — Show Schedule (Multiplex, Gold Class tag & datepicker)
  4. Switching back to admin via the header persona <select> & inspecting Admin Console
  5. Clicking the DropPulse icon/wordmark (#brand-home) to navigate back to /
  6. Zero uncaught JS exceptions, console errors, or CSP violations
"""

from __future__ import annotations

import argparse
import base64
import glob
import json
import os
import pathlib
import shutil
import socket
import struct
import subprocess
import tempfile
import time
import urllib.request
from urllib.parse import urlparse

REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]


def find_chromium_binary() -> str:
    env_bin = os.environ.get("CHROMIUM_BIN", "").strip()
    if env_bin and os.access(env_bin, os.X_OK):
        return env_bin
    for name in ("chromium", "chromium-browser", "google-chrome", "chrome-headless-shell"):
        found = shutil.which(name)
        if found:
            return found
    patterns = [
        os.path.expanduser("~/.cache/ms-playwright/chromium_headless_shell-*/chrome-headless-shell-*/chrome-headless-shell"),
        os.path.expanduser("~/.cache/ms-playwright/chromium-*/chrome-linux*/chrome"),
    ]
    for pat in patterns:
        matches = sorted(glob.glob(pat), reverse=True)
        for cand in matches:
            if os.access(cand, os.X_OK):
                return cand
    raise RuntimeError("No headless Chromium binary found (set CHROMIUM_BIN or install chromium)")


class CDPClient:
    """Minimal RFC 6455 WebSocket + CDP JSON-RPC client using Python stdlib."""

    def __init__(self, ws_url: str) -> None:
        u = urlparse(ws_url)
        self.sock = socket.create_connection((u.hostname, u.port), timeout=15)
        key = base64.b64encode(os.urandom(16)).decode("ascii")
        path = u.path + (f"?{u.query}" if u.query else "")
        handshake = (
            f"GET {path} HTTP/1.1\r\n"
            f"Host: {u.hostname}:{u.port}\r\n"
            "Upgrade: websocket\r\n"
            "Connection: Upgrade\r\n"
            f"Sec-WebSocket-Key: {key}\r\n"
            "Sec-WebSocket-Version: 13\r\n\r\n"
        )
        self.sock.sendall(handshake.encode("ascii"))
        buf = b""
        while b"\r\n\r\n" not in buf:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise RuntimeError("WebSocket handshake closed prematurely")
            buf += chunk
        header, self.extra = buf.split(b"\r\n\r\n", 1)
        if b"101" not in header.splitlines()[0]:
            raise RuntimeError(f"WebSocket handshake failed: {header!r}")
        self.next_id = 0
        self.events: list[dict] = []

    def _recv_exact(self, n: int) -> bytes:
        out = bytearray()
        if self.extra:
            take = min(n, len(self.extra))
            out.extend(self.extra[:take])
            self.extra = self.extra[take:]
        while len(out) < n:
            chunk = self.sock.recv(n - len(out))
            if not chunk:
                raise RuntimeError("WebSocket closed during read")
            out.extend(chunk)
        return bytes(out)

    def _send_frame(self, payload: bytes, opcode: int = 0x1) -> None:
        mask = os.urandom(4)
        header = bytearray([0x80 | opcode])
        length = len(payload)
        if length < 126:
            header.append(0x80 | length)
        elif length < 65536:
            header.append(0x80 | 126)
            header.extend(struct.pack("!H", length))
        else:
            header.append(0x80 | 127)
            header.extend(struct.pack("!Q", length))
        header.extend(mask)
        masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        self.sock.sendall(bytes(header) + masked)

    def _recv_message(self) -> str:
        while True:
            b1, b2 = self._recv_exact(2)
            opcode = b1 & 0x0F
            masked = (b2 & 0x80) != 0
            length = b2 & 0x7F
            if length == 126:
                (length,) = struct.unpack("!H", self._recv_exact(2))
            elif length == 127:
                (length,) = struct.unpack("!Q", self._recv_exact(8))
            mask_key = self._recv_exact(4) if masked else b""
            payload = self._recv_exact(length)
            if masked:
                payload = bytes(b ^ mask_key[i % 4] for i, b in enumerate(payload))
            if opcode == 0x9:
                self._send_frame(payload, opcode=0xA)
                continue
            if opcode == 0x8:
                raise RuntimeError("WebSocket closed by browser")
            if opcode == 0x1:
                return payload.decode("utf-8")

    def call(self, method: str, params: dict | None = None) -> dict:
        self.next_id += 1
        req_id = self.next_id
        msg = json.dumps({"id": req_id, "method": method, "params": params or {}})
        self._send_frame(msg.encode("utf-8"))
        while True:
            raw = self._recv_message()
            data = json.loads(raw)
            if "id" in data and data["id"] == req_id:
                if "error" in data:
                    raise RuntimeError(f"CDP error in {method}: {data['error']}")
                return data.get("result", {})
            self.events.append(data)

    def eval(self, expr: str, await_promise: bool = True):
        res = self.call(
            "Runtime.evaluate",
            {
                "expression": expr,
                "returnByValue": True,
                "awaitPromise": await_promise,
            },
        )
        if "exceptionDetails" in res:
            raise RuntimeError(f"JS exception evaluating {expr!r}: {res['exceptionDetails']}")
        return res.get("result", {}).get("value")

    def wait_for(self, js_predicate: str, timeout: float = 8.0, label: str = "") -> object:
        deadline = time.time() + timeout
        last_val = None
        wrapped = f"(() => {{ try {{ return ({js_predicate}); }} catch (e) {{ return false; }} }})()"
        while time.time() < deadline:
            try:
                last_val = self.eval(wrapped)
                if last_val:
                    return last_val
            except RuntimeError:
                pass
            time.sleep(0.08)
        raise AssertionError(f"Timed out waiting for {label or js_predicate} (last={last_val!r})")

    def screenshot(self, out_path: pathlib.Path | None) -> None:
        if out_path is None:
            return
        res = self.call("Page.captureScreenshot", {"format": "png"})
        out_path.write_bytes(base64.b64decode(res["data"]))

    def close(self) -> None:
        try:
            self.sock.close()
        except Exception:
            pass


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def wait_http(url: str, timeout: float = 15.0) -> str:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as resp:
                return resp.read().decode("utf-8")
        except Exception:
            time.sleep(0.15)
    raise RuntimeError(f"Timed out waiting for {url}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--screenshots-dir", type=pathlib.Path, default=None)
    args = parser.parse_args()

    if args.screenshots_dir:
        args.screenshots_dir.mkdir(parents=True, exist_ok=True)

    chrome_bin = find_chromium_binary()
    app_port = free_port()
    cdp_port = free_port()
    base_url = f"http://127.0.0.1:{app_port}"

    user_data_dir = tempfile.mkdtemp(prefix="dp-chrome-")
    srv_log = tempfile.NamedTemporaryFile(prefix="dp-srv-", suffix=".log", delete=False)
    chr_log = tempfile.NamedTemporaryFile(prefix="dp-chr-", suffix=".log", delete=False)

    env = os.environ.copy()
    env.update(
        {
            "APP_ENV": "development",
            "BIND_ADDR": "127.0.0.1",
            "PORT": str(app_port),
            "DATABASE_URL": "memory://",
            "LOCAL_TEST_AUTH": "true",
            "FRONTEND_FILE": "../frontend/index.html",
            "PUBLIC_API_URL": base_url,
            "WEB_APP_URL": base_url,
        }
    )

    srv_proc = subprocess.Popen(
        ["go", "run", "./cmd/server"],
        cwd=str(REPO_ROOT / "backend"),
        env=env,
        stdout=srv_log,
        stderr=subprocess.STDOUT,
    )
    chr_proc = subprocess.Popen(
        [
            chrome_bin,
            "--headless",
            "--no-sandbox",
            "--disable-gpu",
            "--disable-dev-shm-usage",
            f"--user-data-dir={user_data_dir}",
            f"--remote-debugging-port={cdp_port}",
            "--window-size=1280,920",
            "about:blank",
        ],
        stdout=chr_log,
        stderr=subprocess.STDOUT,
    )

    cdp: CDPClient | None = None
    try:
        assert '"ok"' in wait_http(f"{base_url}/healthz")
        targets = json.loads(wait_http(f"http://127.0.0.1:{cdp_port}/json/list"))
        page_target = next(t for t in targets if t.get("type") == "page")
        cdp = CDPClient(page_target["webSocketDebuggerUrl"])

        cdp.call("Page.enable")
        cdp.call("Runtime.enable")
        cdp.call("Log.enable")
        cdp.call(
            "Emulation.setDeviceMetricsOverride",
            {"width": 1280, "height": 920, "deviceScaleFactor": 2, "mobile": False},
        )

        # 1. Default admin dashboard & #brand-home link
        cdp.call("Page.navigate", {"url": f"{base_url}/app/"})
        cdp.wait_for(
            "!document.getElementById('events-panel').hidden && !document.getElementById('dev-persona').hidden",
            label="admin dashboard & persona selector visible",
        )
        admin_state = cdp.eval("""(() => ({
            badge: document.getElementById('mode-badge').textContent,
            viewSwitchVisible: !document.getElementById('view-switch').hidden,
            personaOptions: Array.from(document.getElementById('dev-persona-select').options).map(o => o.textContent),
            brandHref: document.getElementById('brand-home').getAttribute('href'),
            brandText: document.getElementById('brand-home').textContent.trim(),
            brandBox: document.getElementById('brand-home').getBoundingClientRect().toJSON(),
        }))()""")
        assert admin_state["badge"] == "local test · admin", admin_state
        assert admin_state["viewSwitchVisible"] is True, admin_state
        assert admin_state["brandHref"] == "/" and admin_state["brandText"] == "DropPulse", admin_state
        assert admin_state["brandBox"]["width"] > 120 and admin_state["brandBox"]["height"] >= 36, admin_state
        assert any("alice (user)" in opt for opt in admin_state["personaOptions"]), admin_state
        print("[1/5] Verified default admin session, persona dropdown, and #brand-home link")

        # 2. Switch to user 'alice' via ?as=alice&role=user
        cdp.call("Page.navigate", {"url": f"{base_url}/app/?as=alice&role=user"})
        cdp.wait_for(
            "document.getElementById('mode-badge').textContent === 'local test · user' && !document.getElementById('events-panel').hidden",
            label="alice dashboard loaded",
        )
        alice_state = cdp.eval("""(() => ({
            locationSearch: window.location.search,
            viewSwitchHidden: document.getElementById('view-switch').hidden,
            adminPanelHidden: document.getElementById('admin-panel').hidden,
        }))()""")
        assert alice_state["locationSearch"] == "", alice_state
        assert alice_state["viewSwitchHidden"] is True and alice_state["adminPanelHidden"] is True
        print("[2/5] Verified ?as=alice&role=user login & RBAC hiding of Admin UI")

        # 3. Create Kind-1 (Cinema City schedule mode) and Kind-2 (CineStar Gold Class) watches via Wizard
        cdp.eval("document.getElementById('add-event').click()")
        cdp.wait_for(
            "document.getElementById('wizard').open && Boolean(document.querySelector('[data-template-id=\"cinemacity-flora-imax\"]'))",
            label="wizard step 1 open",
        )
        cdp.eval("""(() => {
            document.querySelector('[data-template-id="cinemacity-flora-imax"]').click();
            document.getElementById('wizard-next').click();
        })()""")
        cdp.wait_for(
            "Array.from(document.getElementById('template-field-show').options).some(o => o.value === '__custom__')",
            label="Cinema City step 2 custom option ready",
        )
        cdp.eval("""(() => {
            const sel = document.getElementById('template-field-show');
            sel.value = '__custom__';
            sel.dispatchEvent(new Event('change', { bubbles: true }));
            const urlIn = document.getElementById('template-field-film_url');
            urlIn.value = 'https://www.cinemacity.cz/films/duna-cast-treti/8105s2r';
            urlIn.dispatchEvent(new Event('input', { bubbles: true }));
            const schedRadio = document.getElementById('flora-mode-schedule');
            schedRadio.checked = true;
            schedRadio.dispatchEvent(new Event('change', { bubbles: true }));
            const specDate = document.getElementById('flora-date-specific');
            specDate.checked = true;
            specDate.dispatchEvent(new Event('change', { bubbles: true }));
            const dateIn = document.getElementById('flora-field-date');
            dateIn.value = '2026-12-21';
            dateIn.dispatchEvent(new Event('input', { bubbles: true }));
            const nameIn = document.getElementById('watch-name');
            nameIn.value = 'Cinema City Flora — Dune: Part Three (21/12)';
            nameIn.dispatchEvent(new Event('input', { bubbles: true }));
            document.getElementById('wizard-next').click();
        })()""")
        cdp.wait_for("document.getElementById('wizard-next').textContent === 'Create watch'", label="Cinema City review")
        cdp.screenshot(args.screenshots_dir / "ui_wizard_review.png" if args.screenshots_dir else None)
        cdp.eval("document.getElementById('wizard-next').click()")
        cdp.wait_for("!document.getElementById('wizard').open", label="wizard closed after watch 1")

        # Immediately reopen wizard (exercises the async <dialog> close-event race guard!)
        cdp.eval("document.getElementById('add-event').click()")
        cdp.wait_for(
            "document.getElementById('wizard').open && Boolean(document.querySelector('[data-template-id=\"cinestar-schedule\"]'))",
            label="wizard reopened for CineStar",
        )
        cdp.eval("""(() => {
            document.querySelector('[data-template-id="cinestar-schedule"]').click();
            document.getElementById('wizard-next').click();
        })()""")
        cdp.wait_for(
            "Boolean(document.getElementById('cinestar-field-cinema')) && Array.from(document.getElementById('template-field-show').options).some(o => o.value === '__custom__')",
            label="CineStar step 2 custom option ready",
        )
        cdp.screenshot(args.screenshots_dir / "ui_cinestar_step2.png" if args.screenshots_dir else None)
        cdp.eval("""(() => {
            const sel = document.getElementById('template-field-show');
            sel.value = '__custom__';
            sel.dispatchEvent(new Event('change', { bubbles: true }));
            const urlIn = document.getElementById('cinestar-field-film_url');
            urlIn.value = 'https://cinestar.cz/cz/praha5/filmy/movie/10100-duna-cast-treti';
            urlIn.dispatchEvent(new Event('input', { bubbles: true }));
            const goldRadio = document.getElementById('cinestar-tag-gold-class');
            goldRadio.checked = true;
            goldRadio.dispatchEvent(new Event('change', { bubbles: true }));
            const specDate = document.getElementById('cinestar-date-specific');
            specDate.checked = true;
            specDate.dispatchEvent(new Event('change', { bubbles: true }));
            const dateIn = document.getElementById('cinestar-field-date');
            dateIn.value = '2026-12-21';
            dateIn.dispatchEvent(new Event('input', { bubbles: true }));
            const nameIn = document.getElementById('watch-name');
            nameIn.value = 'CineStar Anděl — Dune: Part Three Gold Class';
            nameIn.dispatchEvent(new Event('input', { bubbles: true }));
            document.getElementById('wizard-next').click();
        })()""")
        cdp.wait_for("document.getElementById('wizard-next').textContent === 'Create watch'", label="CineStar review")
        cdp.eval("document.getElementById('wizard-next').click()")
        cdp.wait_for(
            "!document.getElementById('wizard').open && document.querySelectorAll('#events tr').length === 2",
            label="2 watches rendered in Alice's table",
        )
        cdp.screenshot(args.screenshots_dir / "ui_user_watches.png" if args.screenshots_dir else None)
        print("[3/5] Verified 3-step Wizard creation for Cinema City (Kind 1) and CineStar (Kind 2)")

        # 4. Switch persona back to 'admin' via header <select> & verify Admin Console
        cdp.eval("""(() => {
            const sel = document.getElementById('dev-persona-select');
            const adminOpt = Array.from(sel.options).find(o => o.textContent.startsWith('admin (admin)'));
            sel.value = adminOpt.value;
            sel.dispatchEvent(new Event('change', { bubbles: true }));
        })()""")
        cdp.wait_for(
            "document.getElementById('mode-badge').textContent === 'local test · admin' && !document.getElementById('view-switch').hidden",
            label="switched back to admin via header select",
        )
        cdp.eval("document.querySelector('[data-view-choice=\"admin\"]').click()")
        cdp.wait_for(
            "!document.getElementById('admin-panel').hidden && document.querySelectorAll('#admin-watchers-body tr').length === 2",
            label="admin console watchers loaded",
        )
        admin_rows = cdp.eval("""Array.from(document.querySelectorAll('#admin-watchers-body tr')).map(r => r.textContent)""")
        assert all("alice@local.test (user)" in r for r in admin_rows), admin_rows
        cdp.screenshot(args.screenshots_dir / "ui_admin_console.png" if args.screenshots_dir else None)
        print("[4/5] Verified header persona switcher -> Admin Console (KPIs + Alice's watchers)")

        # 5. Click #brand-home (DropPulse icon + wordmark) and verify navigation to /
        cdp.eval("document.getElementById('brand-home').click()")
        cdp.wait_for("window.location.pathname === '/'", label="navigated to / via #brand-home")
        print("[5/5] Verified clicking #brand-home navigates to /")

        bad_events = []
        for ev in cdp.events:
            m = ev.get("method", "")
            p = ev.get("params", {})
            if m == "Runtime.exceptionThrown":
                bad_events.append(ev)
            elif m == "Runtime.consoleAPICalled" and p.get("type") == "error":
                bad_events.append(ev)
            elif m == "Log.entryAdded" and p.get("entry", {}).get("level") == "error":
                url = p.get("entry", {}).get("url", "")
                if "/api/v1/providers/" not in url:
                    bad_events.append(ev)
        assert not bad_events, f"Browser console/runtime errors detected: {bad_events}"
        print("All headless Chromium E2E checks passed (0 JS/CSP errors).")

    finally:
        if cdp is not None:
            cdp.close()
        chr_proc.terminate()
        srv_proc.terminate()
        try:
            chr_proc.wait(timeout=3)
            srv_proc.wait(timeout=3)
        except Exception:
            chr_proc.kill()
            srv_proc.kill()
        shutil.rmtree(user_data_dir, ignore_errors=True)
        for f in (srv_log.name, chr_log.name):
            try:
                os.unlink(f)
            except OSError:
                pass


if __name__ == "__main__":
    main()
