# Privacy and data-protection record

Status: internal working record. Last reviewed 2026-11-23. This is not legal
advice; the controller should review it before the public preview opens.

The public notice is the source of truth for data subjects:

| Language | URL | Source |
|---|---|---|
| Czech (authoritative) | `https://dp.ondralinek.cz/privacy/` | [`frontend/public/privacy/index.html`](../../frontend/public/privacy/index.html) |
| English (translation) | `https://dp.ondralinek.cz/privacy/en/` | [`frontend/public/privacy/en/index.html`](../../frontend/public/privacy/en/index.html) |

Keep this record and the public notice in sync when processing changes.

## Controller

| | |
|---|---|
| Controller | Ondřej Linek, individual based in the Czech Republic |
| Contact | `me@ondralinek.cz` |
| Hosting | Hetzner Online GmbH, Falkenstein, Germany (EU) |
| Mail | local Postfix/rspamd on the same host; no third-party delivery service |
| Supervisory authority | ÚOOÚ, Pplk. Sochora 27, 170 00 Prague 7, Czech Republic |

Owner to confirm: whether the controller should be a company or a sole trader
(with name, registered address and identification number), and whether the
public contact address should remain `me@ondralinek.cz`.

## Access model

The service is an invitation-only private preview. `ALLOWED_EMAILS` is an
allowlist of verified addresses; `FinishWeb` refuses every other sign-in before
any account row is created. There is no public registration form, and the
landing page calls the action "Sign in", not "Sign up". The configured list
contains only the controller's own address, so the preview is effectively
single-account until more addresses are added.

The allowlist is an access control, not a marketing list. It is checked against
the `email` and `email_verified` claims returned by the provider. When the list
is non-empty — and `APP_ENV=production` refuses to start without it — an
unverified or missing email never passes. An empty list is a development-only
opening and is never valid in production. Changing the list needs an environment
change and a service restart; there is no allowlist table or admin UI.

## Processing activities (Article 30 record)

### 1. Account creation and sign-in

| | |
|---|---|
| Purpose | Create and authenticate an account, link a provider identity, keep the user signed in |
| Data subjects | Invited users who choose Google or Apple sign-in |
| Categories | Email address; email-verified flag; display name if supplied; avatar URL if Google supplies it; provider name; provider subject ID; account timestamps |
| Legal basis | Contract (Art. 6(1)(b)); legitimate interest in a closed preview (Art. 6(1)(f)) for the allowlist check |
| Recipients | Google Ireland Limited or Apple Distribution International Ltd. (authentication); Hetzner (hosting) |
| Storage | `users`, `user_identities` in PostgreSQL |
| Retention | Life of the account; deleted/anonymised on account deletion, subject to legal-claims retention |

Apple sends a name only on the first authorization, in the `user` POST field;
the current code does not read that field, so Apple accounts have no stored
display name. Google's ID token supplies `name` and `picture`. No password is
ever received or stored.

### 2. Invite-only access control

| | |
|---|---|
| Purpose | Refuse sign-in for addresses that are not invited |
| Data subjects | Every person who attempts sign-in |
| Categories | Email address and email-verified flag from the provider; provider name; refusal outcome in logs |
| Legal basis | Legitimate interest in operating a closed preview and preventing abuse (Art. 6(1)(f)) |
| Recipients | None beyond the identity provider and host |
| Storage | In-memory config (`ALLOWED_EMAILS`); no separate allowlist table |
| Retention | Not stored as a separate record; normal server-log retention applies |

### 3. Watches and availability checks

| | |
|---|---|
| Purpose | Store the pages a user watches and the result of each check |
| Data subjects | Account owners |
| Categories | Watch name, HTTPS URL, check interval, enabled flag; availability state; last-check time, HTTP status, error text; notification state and last notification time/error |
| Legal basis | Contract (Art. 6(1)(b)) |
| Recipients | Hetzner (hosting); the third-party ticket site is contacted with a plain GET, without account data or cookies |
| Storage | `events` in PostgreSQL |
| Retention | Until the watch or account is deleted |

The checker sends no cookies, no account identifiers and no notification
address to the watched site. It resolves the hostname once and dials the public
IP directly; loopback, private and reserved destinations are refused.

### 4. Notification email

| | |
|---|---|
| Purpose | Tell the account owner when a watched page becomes available |
| Data subjects | Account owners with a verified address |
| Categories | Recipient address (the verified account address); watch name; target URL; availability message |
| Legal basis | Contract (Art. 6(1)(b)) |
| Recipients | The local mail server; the destination mail provider of the recipient |
| Storage | No notification body is stored in the application database; the MTA keeps delivery logs |
| Retention | Application state on the `events` row until deletion; MTA logs follow their rotation |

Users cannot currently add arbitrary notification addresses; mail goes only to
the verified account address. Apple private-relay addresses are accepted as the
verified address and mail is delivered to the relay.

### 5. Sessions, CSRF and OAuth flow state

| | |
|---|---|
| Purpose | Keep a browser signed in, protect unsafe requests, bind an OAuth flow to the browser that started it |
| Data subjects | Signed-in users and people in the middle of sign-in |
| Categories | Hashed session tokens; session ID; user agent; IP address; session and revocation timestamps; hashed CSRF token; hashed OAuth state and browser binding; nonce; PKCE verifier; redirect URI; return path; flow expiry |
| Legal basis | Contract (Art. 6(1)(b)); legitimate interest in security (Art. 6(1)(f)) |
| Recipients | Hetzner (hosting) |
| Storage | `sessions`, `session_tokens`, `oauth_flows` |
| Retention | Session: 30 days idle, 90 days absolute by default. OAuth flow: 10 minutes by default, then purged hourly |

Raw session tokens exist only in the browser cookie. The database stores SHA-256
hashes. OAuth state and browser-binding values are stored hashed; the nonce and
PKCE verifier are stored in plaintext for the duration of the flow so the token
exchange can use them.

### 6. Server and access logs

| | |
|---|---|
| Purpose | Operate the service, diagnose errors, detect abuse and investigate incidents |
| Data subjects | Visitors and signed-in users |
| Categories | IP address; request path and method; status; timing; request ID; user agent where the application logs it |
| Legal basis | Legitimate interest in security and operations (Art. 6(1)(f)) |
| Recipients | Hetzner (hosting); journald and nginx logs on the host |
| Storage | Backend JSON logs in journald; nginx access/error logs in `/var/log/nginx` |
| Retention | Limited rotation period; not indefinite. Set and document a concrete retention period before opening the preview |

## Cookies and local storage

| Name | Purpose | Attributes | Lifetime |
|---|---|---|---|
| `__Host-dp_session` | Session | HttpOnly, Secure, SameSite=Lax, Path=/ | 30 days (server session: 30 days idle / 90 days absolute) |
| `__Host-dp_csrf` | CSRF double-submit | Secure, SameSite=Lax, Path=/; readable by the app | 30 days |
| `__Host-dp_oauth` | Bind OAuth flow to browser | HttpOnly, Secure, SameSite=None, Path=/ | 10 minutes |
| `droppulse-theme` (localStorage) | Remember theme | Not sent to the server | Until cleared |

No analytics, advertising or cross-site tracking cookies. The public landing
and privacy pages set no cookies. The session and CSRF cookies are strictly
necessary; no consent banner is used.

## Recipients and processors

| Recipient | Role | Location | Notes |
|---|---|---|---|
| Hetzner Online GmbH | Hosting processor | Falkenstein, Germany (EU) | Server and PostgreSQL; confirm the data-processing terms in the Hetzner account |
| Google Ireland Limited | Identity provider (separate controller for its own processing) | EU/EEA; may transfer under its safeguards | Only when the user chooses Google |
| Apple Distribution International Ltd. | Identity provider (separate controller for its own processing) | EU/EEA; may transfer under its safeguards | Only when the user chooses Apple |
| Local MTA on the host | Mail submission | Germany (EU) | No third-party mail service |

No sale, rental or sharing for advertising. No analytics, ad networks or data
brokers. No AI provider receives data today; if AI-assisted features are added,
update this record and the public notice before enabling them.

## International transfers

The application and database are hosted in Germany (EU). Google and Apple may
process authentication data outside the EU/EEA under their own adequacy
decisions or standard contractual clauses; their privacy policies describe the
safeguards. The controller makes no other transfers on its own initiative.

## Security measures

- TLS and HSTS on the public domain; HTTP redirects to HTTPS.
- Opaque server-side sessions; SHA-256 hashes of session tokens; rotating CSRF
  tokens; HttpOnly/Secure/SameSite cookies.
- Browser-bound OAuth flows with state, nonce and PKCE; exact redirect-URI
  matching; provider signature, issuer, audience, nonce and expiry checks.
- Verified-email-only account linking; an unverified address never becomes the
  account address.
- Per-IP authentication and API rate limits; in-process request limits.
- Allowlisted nginx vhost; read-only method policy and strict rate limits on the
  static preview; no API proxy while the backend is not deployed.
- Backend runs as the unprivileged `agent` account; secrets are read from the
  environment, never from the repository.
- No passwords stored; no payment data processed (billing is not active).

## Data-subject requests

The public notice gives `me@ondralinek.cz` as the contact. The controller should:

1. verify that the request comes from the account address (or ask for enough
   evidence without collecting more data than necessary);
2. answer within one month, extendable by two months for complex requests;
3. for erasure, revoke sessions, delete or anonymise the account and its
   watches, and record what was done;
4. keep a short request log (date, request type, outcome) without retaining the
   request content longer than needed.

Self-service export and deletion are not implemented. Until they are, handle
requests manually with SQL against the account, and revoke sessions first.

## Provider registration checklist

These actions need the owner's Google and Apple accounts; the code and URLs are
ready, but no credentials are in the repository or on the VPS.

### Google

1. Create or open a Google Cloud project.
2. Configure the OAuth consent screen:
   - app name `DropPulse`;
   - user support email and developer contact: an address the owner monitors;
   - app homepage: `https://dp.ondralinek.cz/`;
   - privacy policy: `https://dp.ondralinek.cz/privacy/en/` (the English page
     is easier for reviewers; the Czech page is linked from it);
   - authorized domain: `ondralinek.cz` (the top private domain; it covers
     `dp.ondralinek.cz`);
   - scopes: `openid`, `email`, `profile` — all non-sensitive, so no full
     verification review is expected;
   - while the app is in Testing, add the owner's Gmail address as a test user.
3. Create an OAuth client of type **Web application**.
4. Add the exact redirect URI:
   `https://dp.ondralinek.cz/api/v1/auth/google/callback`.
5. Put `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in the backend environment
   (a mode-0600 env file outside the repository), not in git.
6. The backend builds the redirect URI from `PUBLIC_API_URL`; it must be exactly
   `https://dp.ondralinek.cz`.

### Apple

1. In the Apple Developer account, create an App ID (or use an existing one)
   with **Sign in with Apple** enabled.
2. Create a **Services ID** for the web flow.
3. Configure the Services ID:
   - domain: `dp.ondralinek.cz`; complete Apple's domain verification;
   - return URL:
     `https://dp.ondralinek.cz/api/v1/auth/apple/callback`;
   - scopes: name and email.
4. Create a Sign in with Apple private key (`.p8`); record the key ID and team
   ID.
5. Put `APPLE_CLIENT_ID` (the Services ID), `APPLE_TEAM_ID`, `APPLE_KEY_ID` and
   `APPLE_PRIVATE_KEY` (or `APPLE_PRIVATE_KEY_FILE`) in the backend environment.
6. Apple posts the callback cross-site (`response_mode=form_post`); the backend
   accepts POST and sets the state cookie with `SameSite=None; Secure`.
7. In App Store Connect, add the privacy policy URL and complete the App Privacy
   answers for the iOS app. Apple shares a name only on first authorization; the
   current backend does not retain it.

The callback URLs must match exactly, including the path and scheme. The
current static preview does not proxy `/api/`, so registration alone does not
make sign-in work; the backend must be deployed behind the same origin first.

## Deployment prerequisites for sign-in

The public landing pages link to `/app/` and the backend serves the app there
and the API under `/api/`. That deployment is not live yet:

- the VPS has no Go 1.27 toolchain and this repository's CI only builds/tests,
  so a Linux binary must come from CI or another build pipeline, not from this
  box;
- the static vhost currently answers `/api/` and `/app/` with 404 and has no
  `proxy_pass`;
- no OAuth client credentials are configured;
- the database and service unit for the backend are not installed.

Required environment for the preview once those pieces exist:

```sh
APP_ENV=production
PUBLIC_API_URL=https://dp.ondralinek.cz
WEB_APP_URL=https://dp.ondralinek.cz
ALLOWED_EMAILS='invited@example.com'
ADMIN_EMAILS='owner@example.com'
DATABASE_URL=...
SMTP_FROM=agent@ondralinek.cz
```

Set `ALLOWED_EMAILS` to the controller's address for the first preview and add
addresses only as invitations are made. Do not commit the actual address to the
repository; it belongs in the mode-0600 environment file read by the service.

The backend listens on loopback; nginx proxies `/api/` and `/app/` to it. The
backend now serves the app at both `/` and `/app/`; the public pages link to
`/app/`. Keep the static landing and privacy pages served from `/var/www/dp`,
and give the app its own CSP rather than the static page's `script-src 'none'`
policy. A refused sign-in redirects to `/app/?auth_error=not_invited`; the app
shows a generic invitation message and does not echo the address.

## Open items

- Confirm the controller identity and public contact.
- Confirm Hetzner's data-processing terms and the backup retention, if backups
  exist.
- Set a concrete server-log and mail-log retention period in the public notice
  (it currently says "a limited period").
- Decide whether to store Apple's first-authorization name and separate
  first/last-name fields; the public notice currently says Apple names are not
  stored.
- Have the Czech and English notices reviewed by counsel before opening the
  preview to anyone beyond the owner.
- Add professional translations of the privacy notice if it is offered in more
  languages than Czech and English.
- Add self-service account export and deletion, then update the notice.
