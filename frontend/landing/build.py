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
        "title": "DropPulse — soukromá ukázka",
        "description": "DropPulse sleduje stránku se vstupenkami a dá vám vědět, jakmile se něco uvolní.",
        "eyebrow": "Soukromá ukázka",
        "lede": "Dostupnost vstupenek bez neustálého obnovování.",
        "body": "DropPulse sleduje stránku se vstupenkami a dá vám vědět ve chvíli, kdy se něco uvolní, abyste ji nemuseli pořád načítat znovu.",
        "note_strong": "Přístup je zatím na pozvání.",
        "note_rest": "Přihlásit se mohou pouze pozvané účty; registrace není otevřená.",
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
        "title": "DropPulse — private preview",
        "description": "DropPulse watches a ticket page and tells you the moment something becomes available.",
        "eyebrow": "Private preview",
        "lede": "Ticket availability, without the refresh.",
        "body": "DropPulse watches a ticket page and tells you the moment something becomes available, so you do not have to keep reloading it.",
        "note_strong": "Access is by invitation for now.",
        "note_rest": "Only invited accounts can sign in; registration is closed.",
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
        "title": "DropPulse — private Vorschau",
        "description": "DropPulse beobachtet eine Ticketseite und meldet sich, sobald etwas verfügbar wird.",
        "eyebrow": "Private Vorschau",
        "lede": "Ticketverfügbarkeit – ohne ständiges Neuladen.",
        "body": "DropPulse beobachtet eine Ticketseite und meldet sich, sobald etwas verfügbar wird – du musst die Seite nicht immer wieder neu laden.",
        "note_strong": "Der Zugang ist vorerst nur auf Einladung möglich.",
        "note_rest": "Nur eingeladene Konten können sich anmelden; die Registrierung ist geschlossen.",
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
        "title": "DropPulse — aperçu privé",
        "description": "DropPulse surveille une page de billets et vous prévient dès que quelque chose se libère.",
        "eyebrow": "Aperçu privé",
        "lede": "La disponibilité des billets, sans actualiser.",
        "body": "DropPulse surveille une page de billets et vous prévient dès que quelque chose se libère, sans que vous ayez à recharger la page sans cesse.",
        "note_strong": "L’accès est pour l’instant sur invitation.",
        "note_rest": "Seuls les comptes invités peuvent se connecter ; l’inscription est fermée.",
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
        "title": "DropPulse — vista previa privada",
        "description": "DropPulse vigila una página de entradas y te avisa en cuanto se libera algo.",
        "eyebrow": "Vista previa privada",
        "lede": "Disponibilidad de entradas, sin recargar.",
        "body": "DropPulse vigila una página de entradas y te avisa en cuanto se libera algo, para que no tengas que recargarla una y otra vez.",
        "note_strong": "Por ahora el acceso es por invitation.",
        "note_rest": "Solo las cuentas invitadas pueden iniciar sesión; el registro está cerrado.",
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
        "title": "DropPulse — anteprima privata",
        "description": "DropPulse controlla una pagina di biglietti e ti avvisa appena qualcosa si libera.",
        "eyebrow": "Anteprima privata",
        "lede": "Disponibilità dei biglietti, senza ricaricare.",
        "body": "DropPulse controlla una pagina di biglietti e ti avvisa appena qualcosa si libera, così non devi ricaricarla continuamente.",
        "note_strong": "Per ora l’accesso è su invito.",
        "note_rest": "Solo gli account invitati possono accedere; la registrazione è chiusa.",
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
        "title": "DropPulse — pré-visualização privada",
        "description": "O DropPulse vigia uma página de bilhetes e avisa assim que algo fica disponível.",
        "eyebrow": "Pré-visualização privada",
        "lede": "Disponibilidade de bilhetes, sem recarregar.",
        "body": "O DropPulse vigia uma página de bilhetes e avisa assim que algo fica disponível, para não ter de a recarregar constantemente.",
        "note_strong": "Por agora, o acesso é por convite.",
        "note_rest": "Apenas contas convidadas podem iniciar sessão; o registo está fechado.",
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
        "title": "DropPulse — 非公開プレビュー",
        "description": "DropPulse はチケットページを監視し、空きが出た瞬間にお知らせします。",
        "eyebrow": "非公開プレビュー",
        "lede": "チケットの空き状況を、更新なしで。",
        "body": "DropPulse はチケットページを監視し、空きが出た瞬間にお知らせします。何度も再読み込みする必要はありません。",
        "note_strong": "現在、アクセスは招待制です。",
        "note_rest": "招待されたアカウントのみサインインできます。新規登録は停止しています。",
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
        "title": "DropPulse — 비공개 미리보기",
        "description": "DropPulse는 티켓 페이지를 지켜보고 좌석이 풀리는 순간 알려드립니다.",
        "eyebrow": "비공개 미리보기",
        "lede": "새로고침 없이 확인하는 티켓 예매 가능 여부.",
        "body": "DropPulse는 티켓 페이지를 지켜보고 좌석이 풀리는 순간 알려드립니다. 계속 새로고침할 필요가 없습니다.",
        "note_strong": "현재는 초대를 통해서만 이용할 수 있습니다.",
        "note_rest": "초대된 계정만 로그인할 수 있으며, 신규 가입은 받지 않습니다.",
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
        "title": "DropPulse — закрытое превью",
        "description": "DropPulse следит за страницей билетов и сообщает, как только что-то появляется.",
        "eyebrow": "Закрытое превью",
        "lede": "Наличие билетов — без постоянного обновления.",
        "body": "DropPulse следит за страницей билетов и сообщает, как только что-то появляется, чтобы вам не приходилось перезагружать её снова и снова.",
        "note_strong": "Пока доступ только по приглашению.",
        "note_rest": "Войти могут только приглашённые аккаунты; регистрация закрыта.",
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
        "features_title": "Hlavní funkce a způsob fungování",
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
        "features_title": "Features & How DropPulse Works",
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
        "compliance_p3": "<strong>AI NCII(비동의 성적 영상물) 금지:</strong> Google API 서비스 약관에 따라, DropPulse는 Google API, 사용자 데이터 또는 AI 기술을 사용하여 비동의 성적 영상물(AI NCII)이나 유해 콘텐츠를 생성, 처리 또는 유포하지 않습니다.",
    },
    "ru": {
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
        width: min(840px, 100%);
        padding: clamp(28px, 6vw, 60px);
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
        margin: 0 0 24px;
        padding: 7px 14px;
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
        font-size: clamp(2.5rem, 11vw, 6.5rem);
        font-weight: 760;
        letter-spacing: -0.065em;
        line-height: 0.88;
      }

      .lede {
        max-width: 32ch;
        margin: 22px 0 0;
        color: var(--accent);
        font-size: clamp(1.25rem, 3.8vw, 1.85rem);
        font-weight: 620;
        letter-spacing: -0.025em;
        line-height: 1.2;
      }

      .body {
        max-width: 58ch;
        margin: 20px 0 0;
        color: var(--muted);
        font-size: clamp(1rem, 2.4vw, 1.08rem);
      }

      .actions {
        display: flex;
        flex-wrap: wrap;
        gap: 12px;
        align-items: center;
        margin: 26px 0 0;
      }

      .action {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        min-height: 44px;
        padding: 10px 20px;
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

      .section-title {
        margin: 38px 0 14px;
        font-size: 1.15rem;
        font-weight: 700;
        letter-spacing: -0.015em;
        color: var(--text);
      }

      .features-grid {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
        gap: 12px;
      }

      .feature-card {
        padding: 16px 18px;
        border: 1px solid var(--line);
        border-radius: 16px;
        background: var(--accent-soft);
      }

      .feature-card h3 {
        margin: 0 0 6px;
        font-size: 0.94rem;
        font-weight: 650;
        color: var(--text);
      }

      .feature-card p {
        margin: 0;
        font-size: 0.86rem;
        color: var(--muted);
        line-height: 1.45;
      }

      .compliance-box {
        margin-top: 24px;
        padding: 18px 20px;
        border: 1px solid var(--line);
        border-left: 4px solid var(--accent);
        border-radius: 16px;
        background: var(--surface);
      }

      .compliance-box h3 {
        margin: 0 0 8px;
        font-size: 0.96rem;
        font-weight: 680;
        color: var(--text);
      }

      .compliance-box p {
        margin: 0 0 8px;
        font-size: 0.85rem;
        color: var(--muted);
        line-height: 1.5;
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

      .note {
        margin: 32px 0 0;
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

      @media (max-width: 520px) {
        body { padding: 12px; }
        .shell { padding: 26px 18px; border-radius: 20px; }
        .eyebrow { margin-bottom: 18px; }
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
      <p class="eyebrow"><span class="dot" aria-hidden="true"></span> $eyebrow</p>
      <h1>DropPulse</h1>
      <p class="lede">$lede</p>
      <p class="body">$body</p>

      <div class="actions">
        <a class="action" href="$app">$sign_in</a>
        <a class="action secondary" href="$privacy_href" hreflang="$privacy_lang">$privacy</a>
        <a class="action secondary" href="$terms_href" hreflang="$terms_lang">$terms</a>
      </div>

$features_html

$compliance_html

      <p class="note"><strong>$note_strong</strong> $note_rest</p>
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


def render_sections(code: str) -> tuple[str, str]:
    data = SECTIONS_DATA.get(code, SECTIONS_DATA["en"])
    features_cards = []
    for title, desc in data["features"]:
        features_cards.append(
            f'        <div class="feature-card">\n'
            f'          <h3>{title}</h3>\n'
            f'          <p>{desc}</p>\n'
            f'        </div>'
        )
    features_html = (
        f'      <h2 class="section-title">{data["features_title"]}</h2>\n'
        f'      <div class="features-grid">\n'
        + "\n".join(features_cards)
        + f'\n      </div>'
    )

    compliance_html = (
        f'      <div class="compliance-box">\n'
        f'        <h3>{data["compliance_title"]}</h3>\n'
        f'        <p>{data["compliance_p1"]}</p>\n'
        f'        <p>{data["compliance_p2"]}</p>\n'
        f'        <p>{data["compliance_p3"]}</p>\n'
        f'      </div>'
    )
    return features_html, compliance_html


def render(language: dict) -> str:
    features_html, compliance_html = render_sections(language["code"])
    return PAGE.substitute(
        code=language["code"],
        title=language["title"],
        description=language["description"],
        alternates=alternates(),
        css=CSS,
        eyebrow=language["eyebrow"],
        lede=language["lede"],
        body=language["body"],
        features_html=features_html,
        compliance_html=compliance_html,
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
