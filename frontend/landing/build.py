#!/usr/bin/env python3
"""Generate the public DropPulse landing pages.

The public site is static and self-contained: every page carries its own CSS so
a page load is one request and the strict CSP does not need a stylesheet
allowance. The pages are generated from the copy below rather than copied by
hand, which keeps the language switcher and the sign-in link identical
everywhere.

Czech is the default and lives at `/`; every other language has its own
directory. The generated files are committed, so the VPS needs no Python to
deploy the site.

    python3 frontend/landing/build.py
    python3 frontend/landing/build.py --check
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from string import Template

SITE = "https://dp.ondralinek.cz"
ROOT = Path(__file__).resolve().parents[1] / "public"
PRIVACY_CS = "/privacy/"
PRIVACY_EN = "/privacy/en/"
APP = "/app/"

# Order is deliberate: Czech is the default, English the second preference,
# then the other major European languages, then Japanese, Korean and Russian.
LANGUAGES = [
    {
        "code": "cs",
        "path": "/",
        "name": "Čeština",
        "title": "DropPulse — soukromá ukázka",
        "description": "DropPulse sleduje stránku se vstupenkami a dá vám vědět, jakmile se něco uvolní.",
        "eyebrow": "Soukromá ukázka",
        "lede": "Dostupnost vstupenek bez neustálého obnovování.",
        "body": "DropPulse sleduje stránku se vstupenkami a dá vám vědět ve chvíli, kdy se něco uvolní, abyste ji nemuseli pořád načítat znovu.",
        "note_strong": "Přístup je zatím na pozvání.",
        "note_rest": "Přihlásit se mohou pouze pozvané účty; registrace není otevřená.",
        "sign_in": "Přihlásit se",
        "privacy": "Ochrana osobních údajů",
        "privacy_href": PRIVACY_CS,
        "privacy_lang": "cs",
        "language": "Jazyk",
        "footer": "Statická ukázka",
    },
    {
        "code": "en",
        "path": "/en/",
        "name": "English",
        "title": "DropPulse — private preview",
        "description": "DropPulse watches a ticket page and tells you the moment something becomes available.",
        "eyebrow": "Private preview",
        "lede": "Ticket availability, without the refresh.",
        "body": "DropPulse watches a ticket page and tells you the moment something becomes available, so you do not have to keep reloading it.",
        "note_strong": "Access is by invitation for now.",
        "note_rest": "Only invited accounts can sign in; registration is closed.",
        "sign_in": "Sign in",
        "privacy": "Privacy",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "Language",
        "footer": "Static preview",
    },
    {
        "code": "de",
        "path": "/de/",
        "name": "Deutsch",
        "title": "DropPulse — private Vorschau",
        "description": "DropPulse beobachtet eine Ticketseite und meldet sich, sobald etwas verfügbar wird.",
        "eyebrow": "Private Vorschau",
        "lede": "Ticketverfügbarkeit – ohne ständiges Neuladen.",
        "body": "DropPulse beobachtet eine Ticketseite und meldet sich, sobald etwas verfügbar wird – du musst die Seite nicht immer wieder neu laden.",
        "note_strong": "Der Zugang ist vorerst nur auf Einladung möglich.",
        "note_rest": "Nur eingeladene Konten können sich anmelden; die Registrierung ist geschlossen.",
        "sign_in": "Anmelden",
        "privacy": "Datenschutz",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "Sprache",
        "footer": "Statische Vorschau",
    },
    {
        "code": "fr",
        "path": "/fr/",
        "name": "Français",
        "title": "DropPulse — aperçu privé",
        "description": "DropPulse surveille une page de billets et vous prévient dès que quelque chose se libère.",
        "eyebrow": "Aperçu privé",
        "lede": "La disponibilité des billets, sans actualiser.",
        "body": "DropPulse surveille une page de billets et vous prévient dès que quelque chose se libère, sans que vous ayez à recharger la page sans cesse.",
        "note_strong": "L’accès est pour l’instant sur invitation.",
        "note_rest": "Seuls les comptes invités peuvent se connecter ; l’inscription est fermée.",
        "sign_in": "Se connecter",
        "privacy": "Confidentialité",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "Langue",
        "footer": "Aperçu statique",
    },
    {
        "code": "es",
        "path": "/es/",
        "name": "Español",
        "title": "DropPulse — vista previa privada",
        "description": "DropPulse vigila una página de entradas y te avisa en cuanto se libera algo.",
        "eyebrow": "Vista previa privada",
        "lede": "Disponibilidad de entradas, sin recargar.",
        "body": "DropPulse vigila una página de entradas y te avisa en cuanto se libera algo, para que no tengas que recargarla una y otra vez.",
        "note_strong": "Por ahora el acceso es por invitación.",
        "note_rest": "Solo las cuentas invitadas pueden iniciar sesión; el registro está cerrado.",
        "sign_in": "Iniciar sesión",
        "privacy": "Privacidad",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "Idioma",
        "footer": "Vista previa estática",
    },
    {
        "code": "it",
        "path": "/it/",
        "name": "Italiano",
        "title": "DropPulse — anteprima privata",
        "description": "DropPulse controlla una pagina di biglietti e ti avvisa appena qualcosa si libera.",
        "eyebrow": "Anteprima privata",
        "lede": "Disponibilità dei biglietti, senza ricaricare.",
        "body": "DropPulse controlla una pagina di biglietti e ti avvisa appena qualcosa si libera, così non devi ricaricarla continuamente.",
        "note_strong": "Per ora l’accesso è su invito.",
        "note_rest": "Solo gli account invitati possono accedere; la registrazione è chiusa.",
        "sign_in": "Accedi",
        "privacy": "Privacy",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "Lingua",
        "footer": "Anteprima statica",
    },
    {
        "code": "pt",
        "path": "/pt/",
        "name": "Português",
        "title": "DropPulse — pré-visualização privada",
        "description": "O DropPulse vigia uma página de bilhetes e avisa assim que algo fica disponível.",
        "eyebrow": "Pré-visualização privada",
        "lede": "Disponibilidade de bilhetes, sem recarregar.",
        "body": "O DropPulse vigia uma página de bilhetes e avisa assim que algo fica disponível, para não ter de a recarregar constantemente.",
        "note_strong": "Por agora, o acesso é por convite.",
        "note_rest": "Apenas contas convidadas podem iniciar sessão; o registo está fechado.",
        "sign_in": "Entrar",
        "privacy": "Privacidade",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "Idioma",
        "footer": "Pré-visualização estática",
    },
    {
        "code": "ja",
        "path": "/ja/",
        "name": "日本語",
        "title": "DropPulse — 非公開プレビュー",
        "description": "DropPulse はチケットページを監視し、空きが出た瞬間にお知らせします。",
        "eyebrow": "非公開プレビュー",
        "lede": "チケットの空き状況を、更新なしで。",
        "body": "DropPulse はチケットページを監視し、空きが出た瞬間にお知らせします。何度も再読み込みする必要はありません。",
        "note_strong": "現在、アクセスは招待制です。",
        "note_rest": "招待されたアカウントのみサインインできます。新規登録は停止しています。",
        "sign_in": "サインイン",
        "privacy": "プライバシー",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "言語",
        "footer": "静的プレビュー",
    },
    {
        "code": "ko",
        "path": "/ko/",
        "name": "한국어",
        "title": "DropPulse — 비공개 미리보기",
        "description": "DropPulse는 티켓 페이지를 지켜보고 좌석이 풀리는 순간 알려드립니다.",
        "eyebrow": "비공개 미리보기",
        "lede": "새로고침 없이 확인하는 티켓 예매 가능 여부.",
        "body": "DropPulse는 티켓 페이지를 지켜보고 좌석이 풀리는 순간 알려드립니다. 계속 새로고침할 필요가 없습니다.",
        "note_strong": "현재는 초대를 통해서만 이용할 수 있습니다.",
        "note_rest": "초대된 계정만 로그인할 수 있으며, 신규 가입은 받지 않습니다.",
        "sign_in": "로그인",
        "privacy": "개인정보",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "언어",
        "footer": "정적 미리보기",
    },
    {
        "code": "ru",
        "path": "/ru/",
        "name": "Русский",
        "title": "DropPulse — закрытое превью",
        "description": "DropPulse следит за страницей билетов и сообщает, как только что-то появляется.",
        "eyebrow": "Закрытое превью",
        "lede": "Наличие билетов — без постоянного обновления.",
        "body": "DropPulse следит за страницей билетов и сообщает, как только что-то появляется, чтобы вам не приходилось перезагружать её снова и снова.",
        "note_strong": "Пока доступ только по приглашению.",
        "note_rest": "Войти могут только приглашённые аккаунты; регистрация закрыта.",
        "sign_in": "Войти",
        "privacy": "Конфиденциальность",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "Язык",
        "footer": "Статическое превью",
    },
]

CSS = """
      :root {
        color-scheme: light dark;
        --bg: #f6f7f5;
        --surface: rgba(255, 255, 255, 0.78);
        --text: #17201d;
        --muted: #5e6b66;
        --line: rgba(23, 32, 29, 0.12);
        --accent: #0f766e;
        --accent-soft: rgba(15, 118, 110, 0.12);
        --on-accent: #ffffff;
        --shadow: 0 24px 80px rgba(23, 32, 29, 0.10);
      }

      @media (prefers-color-scheme: dark) {
        :root {
          --bg: #0d1210;
          --surface: rgba(22, 30, 27, 0.82);
          --text: #edf4f0;
          --muted: #9aaba4;
          --line: rgba(237, 244, 240, 0.12);
          --accent: #5eead4;
          --accent-soft: rgba(94, 234, 212, 0.10);
          --on-accent: #06201c;
          --shadow: 0 24px 80px rgba(0, 0, 0, 0.35);
        }
      }

      * { box-sizing: border-box; }

      html { min-height: 100%; }

      body {
        min-height: 100svh;
        margin: 0;
        padding: clamp(20px, 5vw, 56px);
        display: grid;
        place-items: center;
        overflow-x: hidden;
        background:
          radial-gradient(circle at 15% 10%, var(--accent-soft), transparent 38%),
          radial-gradient(circle at 88% 92%, var(--accent-soft), transparent 34%),
          var(--bg);
        color: var(--text);
        font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
        line-height: 1.55;
      }

      .shell {
        width: min(760px, 100%);
        padding: clamp(28px, 7vw, 68px);
        border: 1px solid var(--line);
        border-radius: 28px;
        background: var(--surface);
        box-shadow: var(--shadow);
        backdrop-filter: blur(18px);
      }

      .eyebrow {
        display: inline-flex;
        align-items: center;
        gap: 9px;
        margin: 0 0 28px;
        padding: 7px 12px;
        border: 1px solid var(--line);
        border-radius: 999px;
        color: var(--muted);
        font-size: 0.78rem;
        font-weight: 650;
        letter-spacing: 0.08em;
        text-transform: uppercase;
      }

      .dot {
        width: 8px;
        height: 8px;
        border-radius: 50%;
        background: var(--accent);
        box-shadow: 0 0 0 4px var(--accent-soft);
        animation: breathe 2.8s ease-in-out infinite;
      }

      h1 {
        margin: 0;
        font-size: clamp(2.5rem, 12.8vw, 7.5rem);
        font-weight: 760;
        letter-spacing: -0.065em;
        line-height: 0.88;
      }

      .lede {
        max-width: 26ch;
        margin: 22px 0 0;
        color: var(--accent);
        font-size: clamp(1.25rem, 3.8vw, 1.85rem);
        font-weight: 620;
        letter-spacing: -0.025em;
        line-height: 1.2;
      }

      .body {
        max-width: 52ch;
        margin: 24px 0 0;
        color: var(--muted);
        font-size: clamp(1rem, 2.4vw, 1.08rem);
      }

      .note {
        margin: 36px 0 0;
        padding-top: 24px;
        border-top: 1px solid var(--line);
        color: var(--muted);
        font-size: 0.92rem;
      }

      .note strong { color: var(--text); }

      .actions { margin: 22px 0 0; }

      .action {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        min-height: 46px;
        padding: 11px 20px;
        border-radius: 999px;
        background: var(--accent);
        color: var(--on-accent);
        font-weight: 650;
        text-decoration: none;
        transition: transform 140ms ease, filter 140ms ease;
      }

      .action:hover { filter: brightness(1.06); transform: translateY(-1px); }
      .action:active { transform: translateY(0); }

      a:focus-visible {
        outline: 2px solid var(--accent);
        outline-offset: 3px;
      }

      footer {
        display: flex;
        flex-wrap: wrap;
        justify-content: space-between;
        gap: 8px 24px;
        margin-top: 42px;
        color: var(--muted);
        font-size: 0.8rem;
      }

      footer a { color: var(--muted); text-underline-offset: 2px; }
      footer a:hover { color: var(--text); }

      .langs {
        flex: 1 0 100%;
        display: flex;
        flex-wrap: wrap;
        gap: 6px 16px;
        margin-top: 10px;
        padding-top: 16px;
        border-top: 1px solid var(--line);
      }

      .langs a { text-decoration: none; }
      .langs a:hover { text-decoration: underline; }
      .langs a[aria-current="page"] {
        color: var(--text);
        font-weight: 650;
      }

      code {
        font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        font-size: 0.95em;
      }

      @keyframes breathe {
        0%, 100% { box-shadow: 0 0 0 4px var(--accent-soft); }
        50% { box-shadow: 0 0 0 8px transparent; }
      }

      @media (prefers-reduced-motion: reduce) {
        .dot { animation: none; }
        .action { transition: none; }
      }

      @media (max-width: 520px) {
        body { padding: 12px; }
        .shell { padding: 30px 22px; border-radius: 22px; }
        .eyebrow { margin-bottom: 22px; }
        .lede { max-width: none; }
      }
"""

PAGE = Template("""<!doctype html>
<html lang="$code">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="robots" content="noindex">
    <meta name="color-scheme" content="light dark">
    <meta name="description" content="$description">
    <title>$title</title>
    <link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Crect width='64' height='64' rx='14' fill='%230f766e'/%3E%3Cpath d='M14 34h8l5-12 8 24 5-12h10' fill='none' stroke='%23fff' stroke-width='5' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E">
$alternates
    <style>$css    </style>
  </head>
  <body>
    <main class="shell">
      <p class="eyebrow"><span class="dot" aria-hidden="true"></span> $eyebrow</p>
      <h1>DropPulse</h1>
      <p class="lede">$lede</p>
      <p class="body">$body</p>
      <p class="note"><strong>$note_strong</strong> $note_rest</p>
      <p class="actions"><a class="action" href="$app">$sign_in</a></p>
      <footer>
        <span><code>dp.ondralinek.cz</code></span>
        <span><a href="$privacy_href" hreflang="$privacy_lang">$privacy</a> &middot; 2026</span>
        <nav class="langs" aria-label="$language">
$language_links
        </nav>
      </footer>
    </main>
  </body>
</html>
""")


def alternates() -> str:
    lines = []
    for language in LANGUAGES:
        lines.append(
            '    <link rel="alternate" hreflang="%s" href="%s%s">'
            % (language["code"], SITE, language["path"])
        )
    lines.append('    <link rel="alternate" hreflang="x-default" href="%s/">' % SITE)
    return "\n".join(lines)


def language_links(current: str) -> str:
    links = []
    for language in LANGUAGES:
        current_attr = ' aria-current="page"' if language["code"] == current else ""
        links.append(
            '          <a href="%s" lang="%s" hreflang="%s"%s>%s</a>'
            % (
                language["path"],
                language["code"],
                language["code"],
                current_attr,
                language["name"],
            )
        )
    return "\n".join(links)


def render(language: dict) -> str:
    return PAGE.substitute(
        code=language["code"],
        title=language["title"],
        description=language["description"],
        alternates=alternates(),
        css=CSS,
        eyebrow=language["eyebrow"],
        lede=language["lede"],
        body=language["body"],
        note_strong=language["note_strong"],
        note_rest=language["note_rest"],
        sign_in=language["sign_in"],
        app=APP,
        privacy_href=language["privacy_href"],
        privacy_lang=language["privacy_lang"],
        privacy=language["privacy"],
        language=language["language"],
        language_links=language_links(language["code"]),
    )


def target(language: dict) -> Path:
    if language["path"] == "/":
        return ROOT / "index.html"
    return ROOT / language["path"].strip("/") / "index.html"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="fail if a generated page is missing or out of date",
    )
    args = parser.parse_args()

    failures = 0
    for language in LANGUAGES:
        path = target(language)
        expected = render(language)
        if args.check:
            actual = path.read_text(encoding="utf-8") if path.exists() else ""
            if actual != expected:
                print("out of date: %s" % path.relative_to(ROOT.parent.parent), file=sys.stderr)
                failures += 1
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(expected, encoding="utf-8")
        print("wrote %s" % path.relative_to(ROOT.parent.parent))

    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
