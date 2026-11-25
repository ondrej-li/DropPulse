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
TERMS_CS = "/terms/"
TERMS_EN = "/terms/en/"
APP = "/app/"

# Order is deliberate: Czech is the default, English the second preference,
# then the other major European languages, then Japanese, Korean and Russian.
LANGUAGES = [
    {
        "code": "cs",
        "path": "/",
        "name": "Čeština",
        "title": "DropPulse — Sledování dostupnosti vstupenek",
        "description": "DropPulse sleduje stránky se vstupenkami a okamžitě vám dá vědět e-mailem, jakmile se uvolní místa.",
        "eyebrow": "Sledování dostupnosti vstupenek",
        "lede": "Dostupnost vstupenek bez neustálého obnovování stránek.",
        "body": "DropPulse monitoruje na pozadí veřejné stránky akcí, kin a divadel. Ve chvíli, kdy se uvolní dříve vyprodané vstupenky, obdržíte okamžité e-mailové upozornění.",
        "note_strong": "Přístup je v současné době na pozvání.",
        "note_rest": "Do webové aplikace se mohou přihlásit uživatelé na seznamu pozvaných. Přihlášení je dostupné v horní liště.",
        "sign_in": "Přihlásit se",
        "terms": "Podmínky služby",
        "terms_href": TERMS_CS,
        "terms_lang": "cs",
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
        "title": "DropPulse — Event & Ticket Availability Monitor",
        "description": "DropPulse watches public ticket and event pages in the background and notifies you instantly via email when tickets become available.",
        "eyebrow": "Event & Ticket Availability Monitor",
        "lede": "Ticket availability monitoring, without the refresh.",
        "body": "DropPulse continuously tracks publicly accessible event, cinema, and ticketing pages in the background and sends an immediate email alert the moment seats or tickets are released.",
        "note_strong": "Access is currently in private preview.",
        "note_rest": "Authorized users on the operator's allowlist can access the dashboard using the Sign In button in the navigation bar.",
        "sign_in": "Sign in",
        "terms": "Terms of Service",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
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
        "title": "DropPulse — Ticket- und Event-Verfügbarkeitsmonitor",
        "description": "DropPulse beobachtet Ticketseiten im Hintergrund und meldet sich sofort per E-Mail, sobald Karten verfügbar werden.",
        "eyebrow": "Verfügbarkeitsüberwachung",
        "lede": "Ticketverfügbarkeit – ohne ständiges Neuladen.",
        "body": "DropPulse überwacht öffentlich zugängliche Veranstaltungs- und Ticketseiten im Hintergrund und sendet sofortige E-Mail-Benachrichtigungen, wenn Plätze frei werden.",
        "note_strong": "Zugang derzeit nur auf Einladung.",
        "note_rest": "Eingeladene Benutzer können sich über die Schaltfläche in der Navigationsleiste anmelden.",
        "sign_in": "Anmelden",
        "terms": "Nutzungsbedingungen",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
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
        "title": "DropPulse — Surveillance de disponibilité des billets",
        "description": "DropPulse surveille les billetteries en arrière-plan et vous alerte par e-mail dès que des places se libèrent.",
        "eyebrow": "Surveillance de disponibilité",
        "lede": "La disponibilité des billets, sans actualiser.",
        "body": "DropPulse surveille en arrière-plan les pages publiques de billetterie et vous envoie une alerte e-mail instantanée dès que des places sont remises en vente.",
        "note_strong": "Accès actuellement sur invitation.",
        "note_rest": "Les comptes autorisés peuvent se connecter via le bouton situé dans la barre de navigation.",
        "sign_in": "Se connecter",
        "terms": "Conditions d’utilisation",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
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
        "title": "DropPulse — Monitor de disponibilidad de entradas",
        "description": "DropPulse vigila páginas de entradas en segundo plano y le avisa por correo en cuanto hay disponibilidad.",
        "eyebrow": "Monitor de disponibilidad",
        "lede": "Disponibilidad de entradas, sin recargar.",
        "body": "DropPulse supervisa en segundo plano páginas públicas de espectáculos y eventos y le envía una alerta inmediata por correo electrónico cuando se liberan entradas.",
        "note_strong": "Acceso disponible mediante invitación.",
        "note_rest": "Los usuarios autorizados pueden iniciar sesión mediante el botón en la barra de navegación superior.",
        "sign_in": "Iniciar sesión",
        "terms": "Condiciones del servicio",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
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
        "title": "DropPulse — Monitoraggio disponibilità biglietti",
        "description": "DropPulse controlla le pagine dei biglietti in background e ti avvisa via email non appena si liberano posti.",
        "eyebrow": "Monitoraggio disponibilità",
        "lede": "Disponibilità dei biglietti, senza ricaricare.",
        "body": "DropPulse controlla in background le pagine pubbliche di eventi e biglietti e ti invia un avviso email istantaneo non appena i posti tornano disponibili.",
        "note_strong": "Accesso attualmente su invito.",
        "note_rest": "Gli account autorizzati possono accedere tramite il pulsante nella barra di navigazione in alto.",
        "sign_in": "Accedi",
        "terms": "Termini di servizio",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
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
        "title": "DropPulse — Monitor de disponibilidade de bilhetes",
        "description": "O DropPulse vigia páginas de bilhetes em segundo plano e avisa por e-mail assim que surgem lugares.",
        "eyebrow": "Monitor de disponibilidade",
        "lede": "Disponibilidade de bilhetes, sem recarregar.",
        "body": "O DropPulse vigia em segundo plano páginas públicas de espetáculos e bilhetes, enviando um alerta imediato por e-mail no momento em que os lugares ficam disponíveis.",
        "note_strong": "Acesso atualmente por convite.",
        "note_rest": "Os utilizadores autorizados podem iniciar sessão através do botão na barra de navegação.",
        "sign_in": "Entrar",
        "terms": "Termos de serviço",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
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
        "title": "DropPulse — チケット空き状況監視モニター",
        "description": "DropPulse は公開チケットページを巡回し、座席が空いた瞬間にメールでお知らせします。",
        "eyebrow": "空き状況モニター",
        "lede": "チケットの空き状況を、更新なしで。",
        "body": "DropPulse は公開されているイベントやチケットページをバックグラウンドで監視し、座席やキャンセル枠が解放された瞬間に即座にメールでお知らせします。",
        "note_strong": "現在は招待制プレビューです。",
        "note_rest": "招待されたアカウントは、上部ナビゲーションバーのサインインボタンからログインできます。",
        "sign_in": "サインイン",
        "terms": "利用規約",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
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
        "title": "DropPulse — 티켓 예매 가능 여부 모니터",
        "description": "DropPulse는 티켓 페이지를 백그라운드에서 감시하고 좌석이 오픈되는 순간 이메일로 즉시 알려드립니다.",
        "eyebrow": "예매 가능 여부 모니터",
        "lede": "새로고침 없이 확인하는 티켓 예매 가능 여부.",
        "body": "DropPulse는 공개된 공연 및 티켓 페이지를 지속적으로 모니터링하여 취소표나 추가 좌석이 풀리는 즉시 이메일 알림을 전달합니다.",
        "note_strong": "현재 초대를 통해서만 이용 가능합니다.",
        "note_rest": "초대된 사용자는 상단 탐색 표시줄의 로그인 버튼을 통해 대시보드에 접속할 수 있습니다.",
        "sign_in": "로그인",
        "terms": "이용약관",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
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
        "title": "DropPulse — Мониторинг доступности билетов",
        "description": "DropPulse отслеживает страницы билетов в фоновом режиме и мгновенно сообщает на почту о появлении мест.",
        "eyebrow": "Мониторинг доступности",
        "lede": "Наличие билетов — без постоянного обновления.",
        "body": "DropPulse непрерывно отслеживает общедоступные страницы мероприятий и кинотеатров в фоновом режиме и отправляет письмо, как только билеты появляются в наличии.",
        "note_strong": "В настоящее время доступ только по приглашению.",
        "note_rest": "Приглашённые пользователи могут войти в панель управления через кнопку входа в верхней панели.",
        "sign_in": "Войти",
        "terms": "Условия использования",
        "terms_href": TERMS_EN,
        "terms_lang": "en",
        "privacy": "Конфиденциальность",
        "privacy_href": PRIVACY_EN,
        "privacy_lang": "en",
        "language": "Язык",
        "footer": "Статическое превью",
    },
]

SECTIONS_DATA = {
    "cs": {
        "nav_how": "Jak to funguje",
        "nav_features": "Funkce",
        "nav_compliance": "Soulad s pravidly",
        "btn_explore": "Zjistit, jak to funguje",
        "problem_title": "Proč DropPulse?",
        "problem_desc": "Vstupenky na oblíbené koncerty, divadelní představení i filmová promítání v IMAXu se často vyprodají během několika minut. Řada rezervací se však v následujících dnech ruší a místa se nečekaně vrací do prodeje. Neustálé ruční obnovování stránek v prohlížeči je únavné a nespolehlivé. DropPulse hlídá veřejné stránky se vstupenkami za vás a ve chvíli, kdy se uvolní místo, vám pošle okamžitou zprávu na e-mail.",
        "steps_title": "Jak DropPulse funguje ve 4 krocích",
        "steps": [
            ("1. Zadání URL adresy", "Zadáte odkaz na veřejnou stránku akce, kina či vstupenek, kterou chcete monitorovat."),
            ("2. Kontrola na pozadí", "DropPulse v rozumných intervalech ověřuje dostupnost stránek bez přetěžování cizích serverů."),
            ("3. E-mailové upozornění", "Jakmile se stav změní na volno, ihned obdržíte upozornění s přímým odkazem k nákupu."),
            ("4. Nákup u prodejce", "Otevřete oficiální web prodejce ve svém prohlížeči a vstupenky si bezpečně zakoupíte sami."),
        ],
        "features_title": "Klíčové funkce a principy služby",
        "features": [
            ("Automatizované sledování dostupnosti", "DropPulse na pozadí pravidelně ověřuje veřejně přístupné stránky se vstupenkami a zjišťuje, zda se neuvolnila místa či nové termíny."),
            ("Okamžitá e-mailová upozornění", "Jakmile se vstupenka uvolní, obdržíte zprávu na ověřený e-mail s přímým odkazem na nákup, abyste nemuseli stránku ručně obnovovat."),
            ("Férové monitorování bez obcházení", "Zásadně neprovádíme automatizované luštění CAPTCHA ani obcházení ochran. Při detekci výzvy kontrolu pozastavíme pro ruční ověření."),
            ("Ochrana soukromí a minimální data", "Přihlášení slouží výhradně ke správě sledování a doručení e-mailů. Nepoužíváme reklamní trackery a osobní data neprodáváme."),
        ],
        "compliance_title": "Účel aplikace a soulad s pravidly rozhraní Google API",
        "compliance_p1": "Aplikace <strong>DropPulse</strong> slouží výhradně k automatizovanému sledování dostupnosti veřejně přístupných stránek se vstupenkami a událostmi a zasílání notifikačních e-mailů. Přihlášení přes Google vyžaduje pouze základní rozsah (<code>openid</code>, <code>email</code>, <code>profile</code>) výhradně pro ověření identity oprávněného uživatele a doručení vyžádaných notifikací.",
        "compliance_p2": "Využití a předávání informací získaných z rozhraní Google API přísně dodržuje pravidla <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a> včetně požadavků na omezené použití (Limited Use).",
        "compliance_p3": "<strong>Zákaz generování intimního obsahu (AI NCII):</strong> V plném souladu se smluvními podmínkami rozhraní Google API aplikace DropPulse nevyužívá rozhraní Google API, uživatelská data Google ani technologie AI k vytváření, hostování ani šíření intimního materiálu bez souhlasu (AI NCII), sexuálně explicitního obsahu ani jiného škodlivého materiálu.",
    },
    "en": {
        "nav_how": "How It Works",
        "nav_features": "Features",
        "nav_compliance": "Compliance",
        "btn_explore": "Explore how it works",
        "problem_title": "Why DropPulse?",
        "problem_desc": "High-demand concert tickets, theater plays, and movie screenings (like IMAX) frequently sell out within minutes. Yet reservations are continuously cancelled and seats quietly return to inventory at unpredictable hours. Constantly refreshing browser tabs throughout the day is tedious and unreliable. DropPulse acts as your automated availability sentry—monitoring public event pages around the clock and notifying you the second seats open up.",
        "steps_title": "How DropPulse Works in 4 Steps",
        "steps": [
            ("1. Submit Event URL", "Enter the public URL of the ticket page, movie showing, or festival portal you want to monitor."),
            ("2. Background Checks", "DropPulse checks the target page at balanced intervals without hammering or straining target servers."),
            ("3. Email Notification", "The moment tickets transition from unavailable to open, DropPulse delivers an alert email with a direct booking link."),
            ("4. Purchase Directly", "Click the official vendor link in your alert and finalize your purchase securely in your own browser."),
        ],
        "features_title": "Key Capabilities & Design Principles",
        "features": [
            ("Automated Availability Tracking", "DropPulse monitors publicly accessible event and ticket pages in the background, detecting released seats and cancellations without excessive requests."),
            ("Instant Email Alerts", "The moment tickets or seats become available, DropPulse sends an immediate alert to your verified email with a direct link to the booking page."),
            ("Ethical & Human-First", "DropPulse strictly respects target websites: we do not perform automated CAPTCHA solving. If a challenge is encountered, the check pauses so you can verify manually."),
            ("Privacy & Minimal Data", "Sign-in is used solely to authenticate your account and route alert emails. We use no ad trackers, no analytics pixels, and never sell user data."),
        ],
        "compliance_title": "Application Purpose & Google API Terms of Service Compliance",
        "compliance_p1": "The <strong>DropPulse</strong> application is dedicated to automated event ticket availability tracking and notification delivery. Google Sign-In is used exclusively to authenticate authorized users and deliver email notifications via minimal OAuth scopes (<code>openid</code>, <code>email</code>, <code>profile</code>).",
        "compliance_p2": "DropPulse's use and transfer to any other app of information received from Google APIs adheres to the <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a>, including the Limited Use requirements.",
        "compliance_p3": "<strong>Prohibition on AI-Generated Non-Consensual Intimate Imagery (AI NCII):</strong> In strict compliance with the Google APIs Terms of Service, DropPulse does not use Google APIs, user data, or artificial intelligence systems to generate, create, process, host, or distribute Non-Consensual Intimate Imagery (AI NCII), sexually explicit material, or harmful content.",
    },
    "de": {
        "nav_how": "Funktionsweise",
        "nav_features": "Funktionen",
        "nav_compliance": "Richtlinien",
        "btn_explore": "Funktionsweise entdecken",
        "problem_title": "Warum DropPulse?",
        "problem_desc": "Gefragte Tickets für Konzerte, Theater und Kinovorstellungen sind oft in Minuten ausverkauft. Durch Stornierungen werden jedoch regelmäßig Plätze wieder frei. Manuelles Neuladen ist mühsam und ineffizient. DropPulse überwacht die Seiten automatisch und benachrichtigt Sie sofort per E-Mail.",
        "steps_title": "So funktioniert DropPulse in 4 Schritten",
        "steps": [
            ("1. URL eingeben", "Geben Sie die öffentlich zugängliche URL der Ticket- oder Vorstellungsseite ein."),
            ("2. Prüfung im Hintergrund", "DropPulse prüft die Verfügbarkeit regelmäßig und schonend für den Zielserver."),
            ("3. E-Mail-Alarm", "Sobald Plätze frei werden, erhalten Sie eine Benachrichtigung mit direktem Buchungslink."),
            ("4. Sicher buchen", "Öffnen Sie den Link des Anbieters im Browser und schließen Sie den Kauf direkt ab."),
        ],
        "features_title": "Funktionen & Funktionsweise von DropPulse",
        "features": [
            ("Automatische Verfügbarkeitsprüfung", "DropPulse überwacht öffentlich zugängliche Ticket- und Veranstaltungsseiten im Hintergrund auf freigegebene Plätze."),
            ("Sofortige E-Mail-Benachrichtigungen", "Sobald Tickets verfügbar werden, sendet DropPulse eine Benachrichtigung an Ihre verifizierte E-Mail-Adresse."),
            ("Ethisches Monitoring ohne Umgehung", "Keine automatisierte CAPTCHA-Lösung oder Umgehung von Schutzmaßnahmen. Bei Abfragen pausiert die Prüfung zur manuellen Überprüfung."),
            ("Datenschutz & minimale Daten", "Die Anmeldung dient ausschließlich der Kontoverwaltung und dem Benachrichtigungsversand. Keine Tracker, kein Datenverkauf."),
        ],
        "compliance_title": "Anwendungszweck & Einhaltung der Google-API-Richtlinien",
        "compliance_p1": "Die Anwendung <strong>DropPulse</strong> dient ausschließlich der automatisierten Überwachung von Ticketverfügbarkeiten und dem Versand von Benachrichtigungen. Die Google-Anmeldung erfordert minimale Berechtigungen (<code>openid</code>, <code>email</code>, <code>profile</code>) zur Nutzerauthentifizierung.",
        "compliance_p2": "Die Nutzung von Google-API-Daten durch DropPulse entspricht den <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a> einschließlich der Limited Use-Anforderungen.",
        "compliance_p3": "<strong>Verbot von AI NCII:</strong> In voller Übereinstimmung mit den Google-API-Nutzungsbedingungen nutzt DropPulse keine Google-APIs oder KI-Systeme zur Erstellung oder Verbreitung nicht-einvernehmlicher intimer Bilder (AI NCII) oder schädlicher Inhalte.",
    },
    "fr": {
        "nav_how": "Fonctionnement",
        "nav_features": "Fonctionnalités",
        "nav_compliance": "Conformité",
        "btn_explore": "Découvrir le fonctionnement",
        "problem_title": "Pourquoi DropPulse ?",
        "problem_desc": "Les billets pour les concerts et séances populaires sont souvent complets en quelques minutes. Pourtant, des annulations surviennent régulièrement et des places sont remises en vente. DropPulse surveille ces pages pour vous et vous alerte dès qu'un créneau s'ouvre.",
        "steps_title": "Comment fonctionne DropPulse en 4 étapes",
        "steps": [
            ("1. Soumettre l'URL", "Indiquez l'URL publique de la billetterie ou de la séance à surveiller."),
            ("2. Contrôle en continu", "DropPulse vérifie la disponibilité sans surcharger les serveurs distants."),
            ("3. Alerte e-mail", "Dès que des places se libèrent, vous recevez un e-mail avec le lien direct."),
            ("4. Réservation directe", "Finalisez votre commande en toute sécurité sur le site officiel du vendeur."),
        ],
        "features_title": "Fonctionnalités et fonctionnement de DropPulse",
        "features": [
            ("Suivi automatisé de la disponibilité", "DropPulse surveille en arrière-plan les pages publiques de billetterie pour détecter les places remises en vente."),
            ("Alertes e-mail instantanées", "Dès que des billets se libèrent, DropPulse vous envoie une alerte avec un lien direct vers la réservation."),
            ("Surveillance éthique sans contournement", "Aucune résolution automatisée de CAPTCHA. En cas de défi antibot, la surveillance s'interrompt pour une vérification manuelle."),
            ("Confidentialité et données minimales", "La connexion sert uniquement à gérer vos alertes et envoyer les e-mails. Aucun traqueur publicitaire, aucune revente de données."),
        ],
        "compliance_title": "Finalité de l'application et conformité aux règles Google API",
        "compliance_p1": "L'application <strong>DropPulse</strong> est dédiée au suivi automatisé de la disponibilité des billets et à l'envoi de notifications par e-mail. La connexion Google requiert les autorisations minimales (<code>openid</code>, <code>email</code>, <code>profile</code>).",
        "compliance_p2": "L'utilisation des données reçues des API Google respecte les <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a>, y compris les exigences de Limited Use.",
        "compliance_p3": "<strong>Interdiction des contenus AI NCII:</strong> Conformément aux conditions d'utilisation des API Google, DropPulse n'utilise pas les API Google ni l'IA pour générer ou diffuser des images intimes non consenties (AI NCII) ou des contenus nuisibles.",
    },
    "es": {
        "nav_how": "Cómo funciona",
        "nav_features": "Características",
        "nav_compliance": "Cumplimiento",
        "btn_explore": "Descubra cómo funciona",
        "problem_title": "¿Por qué DropPulse?",
        "problem_desc": "Las entradas para conciertos y proyecciones de cine a menudo se agotan en minutos. Sin embargo, muchas reservas se cancelan y las entradas vuelven a salir a la venta. DropPulse supervisa las páginas por usted y le envía una alerta inmediata cuando se liberan plazas.",
        "steps_title": "Cómo funciona DropPulse en 4 pasos",
        "steps": [
            ("1. Añadir la URL", "Pegue el enlace público de la entrada o evento que desea monitorizar."),
            ("2. Control en segundo plano", "DropPulse consulta la disponibilidad de manera equilibrada y respetuosa."),
            ("3. Aviso por correo", "En cuanto se detecta disponibilidad, recibe un aviso inmediato con enlace directo."),
            ("4. Compra en el proveedor", "Acceda al sitio oficial desde su navegador y adquiera las entradas directamente."),
        ],
        "features_title": "Características y funcionamiento de DropPulse",
        "features": [
            ("Seguimiento automatizado de disponibilidad", "DropPulse supervisa en segundo plano las páginas públicas de entradas para detectar asientos o entradas liberadas."),
            ("Alertas inmediatas por correo electrónico", "En cuanto se liberan entradas, DropPulse envía una alerta a su correo con un enlace directo de compra."),
            ("Monitorización ética sin evasión", "No resolvemos CAPTCHA de forma automatizada. Ante desafíos antibot, el control se pausa para verificación manual."),
            ("Privacidad y datos mínimos", "El inicio de sesión sirve únicamente para gestionar alertas. Sin rastreadores publicitarios ni venta de datos."),
        ],
        "compliance_title": "Propósito de la aplicación y cumplimiento de Google API",
        "compliance_p1": "La aplicación <strong>DropPulse</strong> se dedica exclusivamente a monitorizar la disponibilidad de entradas de eventos y enviar avisos. El inicio de sesión con Google requiere permisos mínimos (<code>openid</code>, <code>email</code>, <code>profile</code>).",
        "compliance_p2": "El uso de la información obtenida a través de las API de Google cumple estrictamente con la <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a> y los requisitos de Limited Use.",
        "compliance_p3": "<strong>Prohibición de AI NCII:</strong> De conformidad con los Términos del servicio de las API de Google, DropPulse no utiliza las API de Google ni sistemas de IA para crear o distribuir imágenes íntimas sin consentimiento (AI NCII) ni contenido perjudicial.",
    },
    "it": {
        "nav_how": "Come funziona",
        "nav_features": "Funzionalità",
        "nav_compliance": "Conformità",
        "btn_explore": "Scopri come funziona",
        "problem_title": "Perché DropPulse?",
        "problem_desc": "I biglietti per concerti ed eventi richiesti vanno spesso esauriti in pochi minuti. Tuttavia, molte prenotazioni vengono annullate e i posti tornano disponibili. DropPulse monitora le pagine pubbliche al posto tuo e ti avvisa non appena si libera un posto.",
        "steps_title": "Come funziona DropPulse in 4 passi",
        "steps": [
            ("1. Inserisci l'URL", "Aggiungi il link pubblico della pagina del biglietto o dello spettacolo da monitorare."),
            ("2. Controlli in background", "DropPulse controlla la disponibilità a intervalli regolari senza sovraccaricare i server."),
            ("3. Notifica via email", "Non appena tornano disponibili posti, ricevi un'email con il link diretto di prenotazione."),
            ("4. Acquisto diretto", "Apri il link del fornitore ufficiale e completa l'acquisto in autonomia nel tuo browser."),
        ],
        "features_title": "Funzionalità e funzionamento di DropPulse",
        "features": [
            ("Monitoraggio automatico della disponibilità", "DropPulse controlla in background le pagine pubbliche di biglietti per rilevare posti o disponibilità rilasciate."),
            ("Notifiche e-mail istantanee", "Non appena i biglietti tornano disponibili, DropPulse invia un avviso alla tua e-mail verificata."),
            ("Monitoraggio etico senza bypass", "Nessuna risoluzione automatica di CAPTCHA. In caso di controlli antibot, la verifica si sospende per il controllo manuale."),
            ("Privacy e dati minimi", "L'accesso serve esclusivamente a gestire i monitoraggi e inviare gli avvisi. Nessun tracciamento pubblicitario."),
        ],
        "compliance_title": "Finalità dell'applicazione e conformità alle API Google",
        "compliance_p1": "L'applicazione <strong>DropPulse</strong> è dedicata al monitoraggio automatico della disponibilità di biglietti e all'invio di notifiche. L'accesso Google richiede permessi minimi (<code>openid</code>, <code>email</code>, <code>profile</code>).",
        "compliance_p2": "L'uso dei dati ricevuti dalle API Google è conforme alle <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a> e ai requisiti di Limited Use.",
        "compliance_p3": "<strong>Divieto di AI NCII:</strong> In piena conformità con i Termini di servizio delle API Google, DropPulse non utilizza le API Google né l'IA per generare o diffondere immagini intime non consensuali (AI NCII) o contenuti dannosi.",
    },
    "pt": {
        "nav_how": "Como funciona",
        "nav_features": "Funcionalidades",
        "nav_compliance": "Conformidade",
        "btn_explore": "Saiba como funciona",
        "problem_title": "Porquê o DropPulse?",
        "problem_desc": "Os bilhetes para grandes concertos e eventos esgotam frequentemente em minutos. No entanto, muitas reservas são canceladas e os lugares voltam a ficar vagos. O DropPulse monitoriza as páginas por si e envia-lhe um alerta por e-mail no instante em que surgem bilhetes.",
        "steps_title": "Como funciona o DropPulse em 4 passos",
        "steps": [
            ("1. Submeter o URL", "Indique a ligação pública da página do evento ou cinema que deseja vigiar."),
            ("2. Verificação em segundo plano", "O DropPulse vigia a disponibilidade de forma equilibrada e sem sobrecarregar o site."),
            ("3. Alerta por e-mail", "Assim que surgem lugares, recebe um e-mail com a ligação direta de compra."),
            ("4. Compra no vendedor oficial", "Abra a ligação oficial no seu navegador e termine a compra com total segurança."),
        ],
        "features_title": "Funcionalidades e funcionamento do DropPulse",
        "features": [
            ("Monitorização automática de disponibilidade", "O DropPulse vigia em segundo plano as páginas públicas de bilhetes para detetar lugares disponíveis."),
            ("Alertas por e-mail imediatos", "Assim que os bilhetes ficam disponíveis, o DropPulse envia um alerta com link direto para a compra."),
            ("Monitorização ética sem contornar proteções", "Não resolvemos CAPTCHAs automaticamente. Perante desafios antibot, a verificação pausa para resolução manual."),
            ("Privacidade e dados mínimos", "O início de sessão serve apenas para gerir alertas e enviar notificações. Sem rastreadores nem venda de dados."),
        ],
        "compliance_title": "Finalidade da aplicação e conformidade com a Google API",
        "compliance_p1": "A aplicação <strong>DropPulse</strong> destina-se à monitorização de bilhetes e envio de alertas. O início de sessão com Google requer permissões mínimas (<code>openid</code>, <code>email</code>, <code>profile</code>).",
        "compliance_p2": "A utilização de dados das APIs Google cumpre a <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a>, incluindo os requisitos de Limited Use.",
        "compliance_p3": "<strong>Proibição de AI NCII:</strong> Em total conformidade com os Termos de Serviço das APIs Google, o DropPulse não utiliza as APIs Google nem IA para criar ou distribuir imagens íntimas não consensuais (AI NCII) ou conteúdos nocivos.",
    },
    "ja": {
        "nav_how": "仕組み",
        "nav_features": "機能",
        "nav_compliance": "利用規約遵守",
        "btn_explore": "仕組みを見る",
        "problem_title": "なぜ DropPulse なのか？",
        "problem_desc": "人気コンサートや IMAX 映画のチケットは数分で完売することがあります。しかし、予約キャンセルにより座席が再放出されることも少なくありません。DropPulse が公開チケットページを監視し、空きが出た瞬間にメールで通知します。",
        "steps_title": "DropPulse の利用手順（4ステップ）",
        "steps": [
            ("1. URLを登録", "監視したいチケットページや上映スケジュールの公開URLを入力します。"),
            ("2. バックグラウンド確認", "対象サーバーに負担をかけないよう適切な間隔で巡回します。"),
            ("3. メール通知", "座席の空きが検知された瞬間に直通リンク付きの通知メールが届きます。"),
            ("4. 公式サイトで購入", "ブラウザで正規販売サイトを開き、安全にチケットを購入できます。"),
        ],
        "features_title": "DropPulse の主な機能と仕組み",
        "features": [
            ("空き状況の自動モニタリング", "DropPulse は公開されているチケットページをバックグラウンドで巡回し、座席の開放を検知します。"),
            ("即時メール通知", "チケットが利用可能になった瞬間に、確認済みメールアドレスへ直接リンク付きのアラートを送信します。"),
            ("セキュリティ保護を回避しない倫理的な設計", "CAPTCHA の自動突破などは行いません。ボット判定画面が出た場合は一時停止し、手動確認を促します。"),
            ("プライバシー重視と最小限のデータ取得", "ログインは監視項目の管理と通知送信用にのみ使用されます。広告トラッカーやデータ販売はありません。"),
        ],
        "compliance_title": "アプリケーションの目的と Google API 利用規約の遵守",
        "compliance_p1": "<strong>DropPulse</strong> は、イベントチケットの空き状況の監視およびメール通知配信を目的としたアプリケーションです。Google ログインは、最小限のスコープ（<code>openid</code>、<code>email</code>、<code>profile</code>）によるユーザー認証にのみ使用されます。",
        "compliance_p2": "Google API から取得した情報の使用は、限定使用（Limited Use）要件を含む <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a> に完全に準拠しています。",
        "compliance_p3": "<strong>AI NCII（同意のない親密な画像）の禁止:</strong> Google API 利用規約に従い、DropPulse は Google API や AI 技術を使用して、同意のない親密な画像（AI NCII）や有害なコンテンツを生成、処理、配布することは一切ありません。",
    },
    "ko": {
        "nav_how": "작동 방식",
        "nav_features": "주요 기능",
        "nav_compliance": "규정 준수",
        "btn_explore": "작동 방식 알아보기",
        "problem_title": "왜 DropPulse인가요?",
        "problem_desc": "인기 콘서트나 영화 티켓은 순식간에 매진됩니다. 하지만 예매 취소로 인해 불시에 잔여석이 발생하곤 합니다. 하루 종일 새로고침하는 대신, DropPulse가 티켓 페이지를 대신 모니터링하여 잔여석이 나오는 순간 이메일로 알려드립니다.",
        "steps_title": "간편한 4단계 이용 방법",
        "steps": [
            ("1. URL 등록", "모니터링하고자 하는 티켓 또는 상영 페이지의 공개 URL을 등록합니다."),
            ("2. 백그라운드 점검", "대상 서버에 부하를 주지 않는 적절한 간격으로 상태를 확인합니다."),
            ("3. 즉시 이메일 알림", "좌석이 열리는 즉시 예매 페이지로 직행할 수 있는 링크를 이메일로 발송합니다."),
            ("4. 안전한 직접 예매", "사용자 브라우저에서 공식 예매처에 접속하여 안전하게 결제를 완료합니다."),
        ],
        "features_title": "DropPulse 주요 기능 및 작동 원리",
        "features": [
            ("자동 티켓 예매 가능 여부 모니터링", "DropPulse는 공개된 티켓 예매 페이지를 백그라운드에서 주기적으로 확인하여 취소표나 추가 좌석 오픈을 감지합니다."),
            ("실시간 이메일 알림", "티켓 예매가 가능해지는 즉시 인증된 이메일로 바로 연결되는 링크와 함께 알림을 발송합니다."),
            ("보안을 우회하지 않는 윤리적 모니터링", "CAPTCHA 자동 해결이나 악의적 우회를 하지 않습니다. 챌린지가 발생하면 확인을 일시 정지하고 직접 확인할 수 있도록 안내합니다."),
            ("개인정보 보호 및 최소 데이터 수집", "로그인은 모니터링 관리 및 알림 전송에만 사용됩니다. 광고 트래커나 데이터 판매가 없습니다."),
        ],
        "compliance_title": "애플리케이션 목적 및 Google API 서비스 약관 준수",
        "compliance_p1": "<strong>DropPulse</strong>는 이벤트 티켓 예매 가능 여부 모니터링 및 이메일 알림을 제공하는 유틸리티입니다. Google 로그인은 최소한의 권한(<code>openid</code>, <code>email</code>, <code>profile</code>)으로 사용자 인증에만 사용됩니다.",
        "compliance_p2": "Google API에서 수신한 정보의 사용 및 전송은 Limited Use 요건을 포함한 <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a>를 엄격히 준수합니다.",
        "compliance_p3": "<strong>AI NCII(비동의 성적 영상物) 금지:</strong> Google API 서비스 약관에 따라, DropPulse는 Google API, 사용자 데이터 또는 AI 기술을 사용하여 비동의 성적 영상물(AI NCII)이나 유해 콘텐츠를 생성, 처리 또는 유포하지 않습니다.",
    },
    "ru": {
        "nav_how": "Как это работает",
        "nav_features": "Возможности",
        "nav_compliance": "Соответствие правилам",
        "btn_explore": "Узнать больше о работе",
        "problem_title": "Зачем нужен DropPulse?",
        "problem_desc": "Билеты на популярные концерты и премьеры часто раскупают за минуты. Однако брони регулярно отменяются, и места снова поступают в продажу. Постоянно обновлять вкладку в браузере неудобно. DropPulse отслеживает страницу за вас и присылает уведомление в момент появления мест.",
        "steps_title": "Принцип работы в 4 шага",
        "steps": [
            ("1. Добавление ссылки", "Укажите общедоступную ссылку на страницу билетов или сеанса для отслеживания."),
            ("2. Проверка в фоне", "DropPulse проверяет доступность с умеренной периодичностью, не перегружая сервер."),
            ("3. Уведомление на почту", "Как только появляются билеты, вы получаете письмо с прямой ссылкой на бронирование."),
            ("4. Покупка на сайте", "Откройте ссылку в своём браузере и безопасно оформите заказ на официальном сайте."),
        ],
        "features_title": "Возможности и принцип работы DropPulse",
        "features": [
            ("Автоматическое отслеживание билетов", "DropPulse в фоновом режиме проверяет общедоступные страницы билетов на появление освободившихся мест."),
            ("Мгновенные уведомления на почту", "Как только появляются билеты, вы сразу получаете письмо со ссылкой на покупку на подтверждённый адрес."),
            ("Этичный мониторинг без обхода защит", "Мы принципиально не занимаемся автоматическим решением CAPTCHA. При возникновении проверки мониторинг приостанавливается."),
            ("Конфиденциальность и минимум данных", "Вход используется исключительно для сохранения параметров отслеживания и отправки уведомлений. Без рекламы и передачи данных."),
        ],
        "compliance_title": "Назначение приложения и соблюдение правил Google API",
        "compliance_p1": "Приложение <strong>DropPulse</strong> предназначено для мониторинга доступности билетов и отправки уведомлений. Вход через Google запрашивает минимальные права (<code>openid</code>, <code>email</code>, <code>profile</code>) только для аутентификации пользователя.",
        "compliance_p2": "Использование данных Google API строго соответствует <a href=\"https://developers.google.com/terms/api-services-user-data-policy\" target=\"_blank\" rel=\"noopener noreferrer\">Google API Services User Data Policy</a>, включая требования Limited Use.",
        "compliance_p3": "<strong>Запрет AI NCII:</strong> В полном соответствии с Условиями использования Google API, DropPulse не использует Google API или технологии ИИ для создания или распространения интимных изображений без согласия (AI NCII) или вредоносных материалов.",
    },
}

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

      html {
        min-height: 100%;
        scroll-behavior: smooth;
      }

      body {
        min-height: 100svh;
        margin: 0;
        padding: clamp(20px, 4vw, 48px);
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
        width: min(880px, 100%);
        padding: clamp(24px, 5vw, 56px);
        border: 1px solid var(--line);
        border-radius: 28px;
        background: var(--surface);
        box-shadow: var(--shadow);
        backdrop-filter: blur(18px);
      }

      .top-bar {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        justify-content: space-between;
        gap: 16px;
        padding-bottom: 22px;
        margin-bottom: 30px;
        border-bottom: 1px solid var(--line);
      }

      .brand {
        display: inline-flex;
        align-items: center;
        gap: 10px;
        color: var(--text);
        font-size: 1.05rem;
        font-weight: 750;
        letter-spacing: -0.01em;
        text-decoration: none;
      }

      .brand-mark {
        display: grid;
        place-items: center;
        width: 30px;
        height: 30px;
        border-radius: 8px;
        background: linear-gradient(135deg, #0f766e, #14b8a6);
        color: #ffffff;
        flex: none;
      }

      .brand-mark svg { display: block; width: 17px; height: 17px; }

      .top-nav {
        display: flex;
        align-items: center;
        gap: 16px;
        font-size: 0.88rem;
      }

      .nav-link {
        color: var(--muted);
        text-decoration: none;
        transition: color 120ms ease;
      }

      .nav-link:hover { color: var(--text); }

      .nav-signin {
        display: inline-flex;
        align-items: center;
        padding: 6px 16px;
        border: 1px solid var(--accent);
        border-radius: 999px;
        background: var(--accent-soft);
        color: var(--accent);
        font-weight: 650;
        text-decoration: none;
        transition: transform 140ms ease, background 140ms ease, color 140ms ease;
      }

      .nav-signin:hover {
        background: var(--accent);
        color: var(--on-accent);
        transform: translateY(-1px);
      }

      .hero {
        margin-bottom: 24px;
      }

      .eyebrow {
        display: inline-flex;
        align-items: center;
        gap: 9px;
        margin: 0 0 20px;
        padding: 6px 14px;
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

      .hero-title {
        margin: 0;
        font-size: clamp(2.5rem, 8vw, 4.6rem);
        font-weight: 780;
        letter-spacing: -0.055em;
        line-height: 0.98;
      }

      .lede {
        max-width: 32ch;
        margin: 20px 0 0;
        color: var(--accent);
        font-size: clamp(1.2rem, 3.4vw, 1.65rem);
        font-weight: 620;
        letter-spacing: -0.02em;
        line-height: 1.25;
      }

      .body {
        max-width: 58ch;
        margin: 18px 0 0;
        color: var(--muted);
        font-size: clamp(0.98rem, 2.2vw, 1.05rem);
      }

      .actions {
        display: flex;
        flex-wrap: wrap;
        gap: 12px;
        align-items: center;
        margin: 24px 0 0;
      }

      .action {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        min-height: 42px;
        padding: 9px 18px;
        border-radius: 999px;
        background: var(--accent);
        color: var(--on-accent);
        font-weight: 650;
        text-decoration: none;
        transition: transform 140ms ease, filter 140ms ease;
      }

      .action:hover { filter: brightness(1.06); transform: translateY(-1px); }
      .action:active { transform: translateY(0); }

      .action.secondary {
        background: transparent;
        color: var(--text);
        border: 1px solid var(--line);
      }

      .action.secondary:hover {
        background: var(--accent-soft);
        filter: none;
      }

      a:focus-visible {
        outline: 2px solid var(--accent);
        outline-offset: 3px;
      }

      .content-section {
        margin-top: 40px;
        padding-top: 32px;
        border-top: 1px solid var(--line);
      }

      .section-title {
        margin: 0 0 12px;
        font-size: 1.22rem;
        font-weight: 700;
        letter-spacing: -0.02em;
        color: var(--text);
      }

      .section-desc {
        margin: 0;
        font-size: 0.95rem;
        color: var(--muted);
        line-height: 1.65;
        max-width: 68ch;
      }

      .steps-grid {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
        gap: 14px;
        margin-top: 18px;
      }

      .step-card {
        padding: 18px 20px;
        border: 1px solid var(--line);
        border-radius: 18px;
        background: var(--surface);
      }

      .step-num {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 26px;
        height: 26px;
        border-radius: 50%;
        background: var(--accent);
        color: var(--on-accent);
        font-size: 0.8rem;
        font-weight: 700;
        margin-bottom: 10px;
      }

      .step-card h3 {
        margin: 0 0 6px;
        font-size: 0.95rem;
        font-weight: 680;
        color: var(--text);
      }

      .step-card p {
        margin: 0;
        font-size: 0.86rem;
        color: var(--muted);
        line-height: 1.5;
      }

      .features-grid {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
        gap: 14px;
        margin-top: 18px;
      }

      .feature-card {
        padding: 18px 20px;
        border: 1px solid var(--line);
        border-radius: 18px;
        background: var(--accent-soft);
      }

      .feature-card h3 {
        margin: 0 0 6px;
        font-size: 0.95rem;
        font-weight: 680;
        color: var(--text);
      }

      .feature-card p {
        margin: 0;
        font-size: 0.86rem;
        color: var(--muted);
        line-height: 1.5;
      }

      .compliance-box {
        padding: 22px 24px;
        border: 1px solid var(--line);
        border-left: 4px solid var(--accent);
        border-radius: 18px;
        background: var(--surface);
      }

      .compliance-box h3 {
        margin: 0 0 10px;
        font-size: 1.02rem;
        font-weight: 680;
        color: var(--text);
      }

      .compliance-box p {
        margin: 0 0 10px;
        font-size: 0.88rem;
        color: var(--muted);
        line-height: 1.6;
      }

      .compliance-box p:last-child {
        margin-bottom: 0;
      }

      .compliance-box strong { color: var(--text); }
      .compliance-box a { color: var(--accent); text-underline-offset: 2px; }
      .compliance-box code {
        padding: 0.1em 0.35em;
        border: 1px solid var(--line);
        border-radius: 4px;
        font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
        font-size: 0.88em;
      }

      .preview-notice {
        margin-top: 32px;
      }

      .note {
        margin: 0;
        padding-top: 20px;
        border-top: 1px solid var(--line);
        color: var(--muted);
        font-size: 0.9rem;
      }

      .note strong { color: var(--text); }

      footer {
        display: flex;
        flex-wrap: wrap;
        justify-content: space-between;
        gap: 8px 24px;
        margin-top: 36px;
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

      @media (max-width: 640px) {
        body { padding: 12px; }
        .shell { padding: 24px 18px; border-radius: 20px; }
        .top-bar { flex-direction: column; align-items: flex-start; gap: 12px; }
        .top-nav { flex-wrap: wrap; gap: 10px 14px; width: 100%; justify-content: space-between; }
        .eyebrow { margin-bottom: 16px; }
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
    <link rel="icon" type="image/png" sizes="120x120" href="/logo.png">
    <link rel="apple-touch-icon" href="/logo.png">
$alternates
    <style>$css    </style>
  </head>
  <body>
    <main class="shell">
      <header class="top-bar">
        <a class="brand" href="$path">
          <span class="brand-mark" aria-hidden="true">
            <svg viewBox="0 0 32 32" fill="none">
              <path d="M6 17h5l2.6-6.2L18.4 23l2.6-6H26" stroke="currentColor" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </span>
          DropPulse
        </a>
        <nav class="top-nav" aria-label="Navigation">
          <a href="#how-it-works" class="nav-link">$nav_how</a>
          <a href="#features" class="nav-link">$nav_features</a>
          <a href="#compliance" class="nav-link">$nav_compliance</a>
          <a href="$app" class="nav-signin">$sign_in</a>
        </nav>
      </header>

      <section class="hero">
        <p class="eyebrow"><span class="dot" aria-hidden="true"></span> $eyebrow</p>
        <h1 class="hero-title">DropPulse</h1>
        <p class="lede">$lede</p>
        <p class="body">$body</p>

        <div class="actions">
          <a class="action" href="#how-it-works">$btn_explore</a>
          <a class="action secondary" href="$privacy_href" hreflang="$privacy_lang">$privacy</a>
          <a class="action secondary" href="$terms_href" hreflang="$terms_lang">$terms</a>
        </div>
      </section>

      <section id="why" class="content-section">
        <h2 class="section-title">$problem_title</h2>
        <p class="section-desc">$problem_desc</p>
      </section>

      <section id="how-it-works" class="content-section">
        <h2 class="section-title">$steps_title</h2>
        <div class="steps-grid">
$steps_html
        </div>
      </section>

      <section id="features" class="content-section">
        <h2 class="section-title">$features_title</h2>
        <div class="features-grid">
$features_html
        </div>
      </section>

      <section id="compliance" class="content-section">
        <div class="compliance-box">
          <h3>$compliance_title</h3>
          <p>$compliance_p1</p>
          <p>$compliance_p2</p>
          <p>$compliance_p3</p>
        </div>
      </section>

      <section class="preview-notice">
        <p class="note"><strong>$note_strong</strong> $note_rest</p>
      </section>

      <footer>
        <span><code>dp.ondralinek.cz</code></span>
        <span><a href="$terms_href" hreflang="$terms_lang">$terms</a> &middot; <a href="$privacy_href" hreflang="$privacy_lang">$privacy</a> &middot; 2026</span>
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


def render_sections(code: str) -> dict[str, str]:
    data = SECTIONS_DATA.get(code, SECTIONS_DATA["en"])

    steps_cards = []
    for i, (title, desc) in enumerate(data["steps"], start=1):
        steps_cards.append(
            f'          <div class="step-card">\n'
            f'            <span class="step-num" aria-hidden="true">{i}</span>\n'
            f'            <h3>{title}</h3>\n'
            f'            <p>{desc}</p>\n'
            f'          </div>'
        )

    features_cards = []
    for title, desc in data["features"]:
        features_cards.append(
            f'          <div class="feature-card">\n'
            f'            <h3>{title}</h3>\n'
            f'            <p>{desc}</p>\n'
            f'          </div>'
        )

    return {
        "nav_how": data["nav_how"],
        "nav_features": data["nav_features"],
        "nav_compliance": data["nav_compliance"],
        "btn_explore": data["btn_explore"],
        "problem_title": data["problem_title"],
        "problem_desc": data["problem_desc"],
        "steps_title": data["steps_title"],
        "steps_html": "\n".join(steps_cards),
        "features_title": data["features_title"],
        "features_html": "\n".join(features_cards),
        "compliance_title": data["compliance_title"],
        "compliance_p1": data["compliance_p1"],
        "compliance_p2": data["compliance_p2"],
        "compliance_p3": data["compliance_p3"],
    }


def render(language: dict) -> str:
    sections = render_sections(language["code"])
    return PAGE.substitute(
        code=language["code"],
        path=language["path"],
        title=language["title"],
        description=language["description"],
        alternates=alternates(),
        css=CSS,
        eyebrow=language["eyebrow"],
        lede=language["lede"],
        body=language["body"],
        nav_how=sections["nav_how"],
        nav_features=sections["nav_features"],
        nav_compliance=sections["nav_compliance"],
        btn_explore=sections["btn_explore"],
        problem_title=sections["problem_title"],
        problem_desc=sections["problem_desc"],
        steps_title=sections["steps_title"],
        steps_html=sections["steps_html"],
        features_title=sections["features_title"],
        features_html=sections["features_html"],
        compliance_title=sections["compliance_title"],
        compliance_p1=sections["compliance_p1"],
        compliance_p2=sections["compliance_p2"],
        compliance_p3=sections["compliance_p3"],
        note_strong=language["note_strong"],
        note_rest=language["note_rest"],
        sign_in=language["sign_in"],
        app=APP,
        terms_href=language["terms_href"],
        terms_lang=language["terms_lang"],
        terms=language["terms"],
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
