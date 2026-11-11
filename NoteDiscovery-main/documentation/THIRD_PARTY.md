# Third-Party Browser Libraries

NoteDiscovery serves every JavaScript and CSS library from its own `/static/vendor/`
path instead of a public CDN. This means:

- **The app works offline** — air-gapped networks, LAN-only installs and internet
  outages no longer break the editor. Previously the UI would not render at all
  without internet, because both the Markdown parser and the UI framework were
  remote. Downloadable HTML exports are the exception, see
  [Known gap](#known-gap-downloadable-html-exports).
- **No third-party requests** — your browser never discloses your IP address or
  the page you are on to jsdelivr, cdnjs or unpkg.
- **No supply-chain exposure to CDNs** — a compromised CDN cannot inject code
  into an app that holds all of your notes.
- **Immune to CDN changes** — pinned versions cannot be moved or removed
  upstream.

## What is bundled

| Library | Version | Licence | Purpose |
|---------|---------|---------|---------|
| [Tailwind CSS](https://tailwindcss.com) | 3.4.17 | MIT | UI styling |
| [Alpine.js](https://alpinejs.dev) | 3.14.1 | MIT | UI reactivity |
| [marked](https://marked.js.org) | 12.0.2 | MIT | Markdown parsing |
| [DOMPurify](https://github.com/cure53/DOMPurify) | 3.0.8 | Apache-2.0 | HTML sanitisation (XSS prevention) |
| [MathJax](https://www.mathjax.org) | 3.2.2 | Apache-2.0 | LaTeX math rendering |
| [highlight.js](https://highlightjs.org) | 11.9.0 | BSD-3-Clause | Code syntax highlighting |
| [Mermaid](https://mermaid.js.org) | 11.12.2 | MIT | Diagram rendering |
| [vis-network](https://visjs.github.io/vis-network/docs/network/) | 9.1.9 | Apache-2.0 | Graph view |
| [qrcode-generator](https://github.com/kazuhikoarase/qrcode-generator) | 1.4.4 | MIT | QR codes for share links |

Total footprint is about 13 MB, dominated by MathJax's web fonts and Mermaid's
per-diagram chunks.

DOMPurify is dual-licensed Apache-2.0 OR MPL-2.0, and vis-network is
dual-licensed Apache-2.0 OR MIT. Both are taken under Apache-2.0.

All of these licences are permissive and compatible with NoteDiscovery's own MIT
licence. None of them is copyleft, and none places any requirement on your notes
or on how you deploy the app.

Apache-2.0 additionally requires redistributing an upstream `NOTICE` file where
one exists. None of the Apache-2.0 libraries above ships one (checked against the
published packages for the exact pinned versions), so the licence texts we
include are sufficient.

One non-library asset is served locally for the same reason:
`frontend/pikapods-run-button.svg` is PikaPods' own deploy button, kept here so
that opening Settings does not fetch an image from them. It is their brand asset
and is used only to link to them.

## How the files get there

The libraries are **not committed to this repository**. `scripts/vendor_assets.py`
downloads them from pinned versions, and `scripts/vendor_lock.json` records a
SHA-256 for every artefact so a download that does not match what was pinned is
rejected rather than installed.

- **Docker** — a dedicated build stage runs the script, so the image ships
  complete and the container never needs internet for the UI.
- **Source installs** — `run.py` runs the script automatically on first start.
  It needs internet that one time; afterwards it is a no-op.

Each library is placed in `frontend/vendor/<library>/` together with its upstream
`LICENSE` file, and `frontend/vendor/THIRD_PARTY_NOTICES.md` indexes the lot.
Those files are part of every distributed artefact, which is what the MIT, BSD
and Apache licences ask for.

## Working with it

```bash
# Download anything missing (safe to re-run; skips what is already correct)
python scripts/vendor_assets.py

# Verify without touching the network, e.g. in CI
python scripts/vendor_assets.py --check

# Re-download everything from scratch
python scripts/vendor_assets.py --force
```

To upgrade a library, edit its version in `scripts/vendor_assets.py`, then:

```bash
python scripts/vendor_assets.py --force --update-lock
```

This refreshes the pinned hashes. Review the resulting `vendor_lock.json` diff
before committing it, since it is the record of exactly what users will receive.

## Known gap: downloadable HTML exports

A downloaded export is the one page that still loads from CDNs, deliberately: it
is meant to be a portable single file that renders on a machine with no access to
your NoteDiscovery server, so pointing it at `/static/vendor/` would make it
*less* portable, not more. The trade-off is that an exported file needs internet
to render maths, diagrams and syntax highlighting. Inlining ~13 MB into every
export is the only alternative that would be truly self-contained.

Everything served by the instance itself uses the vendored copies, including
shared links (`/share/<token>`) and the print preview. Both are produced by
`generate_export_html()` in `backend/export.py`, which switches on its
`local_assets` argument. The CDN versions are pinned to match the vendored ones,
so a note renders the same either way.
