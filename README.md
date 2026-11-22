# DropPulse

DropPulse is organised as three elements in one repository:

| Element | Directory | Stack |
|---|---|---|
| Backend | [`backend/`](backend/) | Go, standard library |
| Frontend | [`frontend/`](frontend/) | Plain HTML and CSS |
| Mobile app | [`mobile/`](mobile/) | iOS and Android |

The backend owns the API. The frontend and both mobile apps are clients of it.

## Running locally

The backend listens on `:8080` unless `PORT` is set:

```sh
cd backend
go run .
```

`GET /healthz` answers with `{"status":"ok"}`.

The frontend is static — open `frontend/index.html` in a browser, or serve the
directory:

```sh
cd frontend
python3 -m http.server 8000
```

The mobile apps are placeholders; see [`mobile/ios/`](mobile/ios/) and
[`mobile/android/`](mobile/android/).

## CI

`.github/workflows/ci.yml` builds, vets and tests the backend on every pull
request. Nothing is built on the VPS.
