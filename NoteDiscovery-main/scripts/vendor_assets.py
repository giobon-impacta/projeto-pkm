#!/usr/bin/env python3
"""Download the frontend's browser libraries into frontend/vendor/.

Run by the Docker build and by run.py on first start. Stdlib-only, so it works
inside python:*-slim with nothing installed but Python.

    python scripts/vendor_assets.py              # download whatever is missing
    python scripts/vendor_assets.py --check      # exit 1 if incomplete, no network
    python scripts/vendor_assets.py --force      # re-download everything
    python scripts/vendor_assets.py --update-lock  # refresh pinned hashes
"""

import argparse
import hashlib
import io
import json
import shutil
import sys
import tarfile
import time
import urllib.error
import urllib.request
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_DEST = REPO_ROOT / "frontend" / "vendor"
LOCK_PATH = Path(__file__).resolve().parent / "vendor_lock.json"
MANIFEST_NAME = "MANIFEST.json"
NOTICES_NAME = "THIRD_PARTY_NOTICES.md"

USER_AGENT = "notediscovery-vendor/1.0 (+https://github.com/gamosoft/NoteDiscovery)"

HLJS_VERSION = "11.9.0"
HLJS_BASE = f"https://cdnjs.cloudflare.com/ajax/libs/highlight.js/{HLJS_VERSION}"
# Kept in sync with the <script> tags in frontend/index.html.
HLJS_LANGUAGES = [
    "csharp", "python", "bash", "shell", "powershell", "javascript",
    "typescript", "json", "sql", "yaml", "css", "http",
]
# Light and dark themes swapped at runtime by app.js.
HLJS_STYLES = ["github.min.css", "github-dark.min.css"]

# Each package is either `files` (direct downloads) or `npm` (a registry tarball
# we extract members from). `license_url` is pinned to the matching tag so the
# notice can't drift from the code; npm packages take theirs from the tarball.
PACKAGES = [
    {
        "name": "tailwindcss",
        "version": "3.4.17",
        "license": "MIT",
        "license_url": "https://raw.githubusercontent.com/tailwindlabs/tailwindcss/v3.4.17/LICENSE",
        "files": {"tailwind.min.js": "https://cdn.tailwindcss.com/3.4.17"},
    },
    {
        "name": "alpinejs",
        "version": "3.14.1",
        "license": "MIT",
        "license_url": "https://raw.githubusercontent.com/alpinejs/alpine/v3.14.1/LICENSE.md",
        "files": {"cdn.min.js": "https://cdn.jsdelivr.net/npm/alpinejs@3.14.1/dist/cdn.min.js"},
    },
    {
        "name": "marked",
        "version": "12.0.2",
        "license": "MIT",
        "license_url": "https://raw.githubusercontent.com/markedjs/marked/v12.0.2/LICENSE.md",
        "files": {"marked.min.js": "https://cdn.jsdelivr.net/npm/marked@12.0.2/marked.min.js"},
    },
    {
        "name": "dompurify",
        "version": "3.0.8",
        # Dual Apache-2.0 OR MPL-2.0; Apache-2.0 avoids MPL source obligations
        "license": "Apache-2.0",
        "license_url": "https://raw.githubusercontent.com/cure53/DOMPurify/3.0.8/LICENSE",
        "files": {"purify.min.js": "https://cdn.jsdelivr.net/npm/dompurify@3.0.8/dist/purify.min.js"},
    },
    {
        "name": "vis-network",
        "version": "9.1.9",
        # Dual Apache-2.0 OR MIT
        "license": "Apache-2.0",
        "license_url": "https://raw.githubusercontent.com/visjs/vis-network/v9.1.9/LICENSE-APACHE-2.0",
        "files": {
            "vis-network.min.js": "https://unpkg.com/vis-network@9.1.9/standalone/umd/vis-network.min.js"
        },
    },
    {
        "name": "qrcode-generator",
        "version": "1.4.4",
        "license": "MIT",
        "license_url": "https://raw.githubusercontent.com/kazuhikoarase/qrcode-generator/master/LICENSE",
        "files": {"qrcode.min.js": "https://cdn.jsdelivr.net/npm/qrcode-generator@1.4.4/qrcode.min.js"},
    },
    {
        "name": "highlight.js",
        "version": HLJS_VERSION,
        "license": "BSD-3-Clause",
        "license_url": f"https://raw.githubusercontent.com/highlightjs/highlight.js/{HLJS_VERSION}/LICENSE",
        "files": {
            "highlight.min.js": f"{HLJS_BASE}/highlight.min.js",
            **{
                f"languages/{lang}.min.js": f"{HLJS_BASE}/languages/{lang}.min.js"
                for lang in HLJS_LANGUAGES
            },
            **{f"styles/{style}": f"{HLJS_BASE}/styles/{style}" for style in HLJS_STYLES},
        },
    },
    {
        "name": "mathjax",
        "version": "3.2.2",
        "license": "Apache-2.0",
        "npm": "mathjax",
        "license_member": "package/LICENSE",
        # Fonts and TeX extensions resolve relative to the entry point's URL
        "members": {
            "tex-mml-chtml.js": "package/es5/tex-mml-chtml.js",
            "output/chtml/fonts/woff-v2/": "package/es5/output/chtml/fonts/woff-v2/",
            "input/tex/extensions/": "package/es5/input/tex/extensions/",
        },
    },
    {
        "name": "mermaid",
        "version": "11.12.2",
        "license": "MIT",
        "npm": "mermaid",
        "license_member": "package/LICENSE",
        # The entry point dynamically imports one chunk per diagram type
        "members": {
            "mermaid.esm.min.mjs": "package/dist/mermaid.esm.min.mjs",
            "chunks/": "package/dist/chunks/",
        },
    },
]


def log(message):
    print(f"[vendor] {message}", flush=True)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def fetch(url, attempts=3):
    """GET a URL, retrying transient failures with a short backoff."""
    last_error = None
    for attempt in range(1, attempts + 1):
        try:
            request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
            with urllib.request.urlopen(request, timeout=60) as response:
                return response.read()
        except (urllib.error.URLError, OSError) as error:
            last_error = error
            if attempt < attempts:
                log(f"  retry {attempt}/{attempts - 1} for {url}: {error}")
                time.sleep(2 * attempt)
    raise RuntimeError(f"Could not download {url}: {last_error}")


def npm_tarball_url(package, version):
    return f"https://registry.npmjs.org/{package}/-/{package}-{version}.tgz"


def expected_lock_entry(spec):
    """Hash keys a package contributes, so --check can spot version bumps."""
    if "npm" in spec:
        return {"version": spec["version"], "keys": ["tarball"]}
    return {"version": spec["version"], "keys": sorted(spec["files"])}


def write_file(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)


def vendor_direct(spec, package_dir, lock_entry, force):
    """Download individually-hosted files, one request each."""
    hashes = {}
    for relative_path, url in sorted(spec["files"].items()):
        target = package_dir / relative_path
        known_hash = lock_entry.get("hashes", {}).get(relative_path)
        if not force and target.exists() and known_hash:
            if sha256(target.read_bytes()) == known_hash:
                hashes[relative_path] = known_hash
                continue
        data = fetch(url)
        digest = sha256(data)
        if known_hash and digest != known_hash:
            raise RuntimeError(
                f"Hash mismatch for {url}\n  expected {known_hash}\n  got      {digest}\n"
                "Refusing to vendor unexpected content. Re-run with --update-lock "
                "if the upstream file legitimately changed."
            )
        write_file(target, data)
        hashes[relative_path] = digest
        log(f"  {relative_path} ({len(data) / 1024:.1f} KB)")
    return hashes


def vendor_npm(spec, package_dir, lock_entry, force):
    """Extract selected trees out of a registry tarball."""
    known_hash = lock_entry.get("hashes", {}).get("tarball")
    marker = package_dir / ".complete"
    if not force and marker.exists() and known_hash and marker.read_text().strip() == known_hash:
        return {"tarball": known_hash}

    url = npm_tarball_url(spec["npm"], spec["version"])
    archive = fetch(url)
    digest = sha256(archive)
    if known_hash and digest != known_hash:
        raise RuntimeError(
            f"Hash mismatch for {url}\n  expected {known_hash}\n  got      {digest}\n"
            "Refusing to vendor unexpected content. Re-run with --update-lock "
            "if the upstream tarball legitimately changed."
        )

    if package_dir.exists():
        shutil.rmtree(package_dir)

    extracted = 0
    extracted_bytes = 0
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
        for member in tar.getmembers():
            if not member.isfile():
                continue
            # Source maps would multiply the image size for devtools-only value
            if member.name.endswith(".map"):
                continue
            target = resolve_member(member.name, spec["members"], package_dir)
            if target is None:
                continue
            handle = tar.extractfile(member)
            if handle is None:
                continue
            data = handle.read()
            write_file(target, data)
            extracted += 1
            extracted_bytes += len(data)

        license_member = spec.get("license_member")
        if license_member:
            try:
                handle = tar.extractfile(license_member)
            except KeyError:
                handle = None
            if handle is not None:
                write_file(package_dir / "LICENSE", handle.read())

    if extracted == 0:
        raise RuntimeError(
            f"Extracted nothing for {spec['name']} — the tarball layout changed."
        )

    log(f"  {extracted} files ({extracted_bytes / 1024 / 1024:.1f} MB) from npm tarball")
    marker.write_text(digest, encoding="utf-8")
    return {"tarball": digest}


def resolve_member(member_name, members, package_dir):
    """Map a tar member onto its vendored path, or None if we don't want it.

    A trailing slash means "this whole tree"; anything else is an exact match.
    """
    for destination, source in members.items():
        if source.endswith("/"):
            if member_name.startswith(source):
                return package_dir / destination / member_name[len(source):]
        elif member_name == source:
            return package_dir / destination
    return None


def build_manifest(results):
    return {
        "generated_by": "scripts/vendor_assets.py",
        "packages": {
            name: {"version": version, "license": license_id, "hashes": hashes}
            for name, version, license_id, hashes in results
        },
    }


def write_notices(dest, results):
    """Index of everything vendored, next to the per-package LICENSE files."""

    lines = [
        "# Third-party notices",
        "",
        "The browser libraries below are redistributed unmodified inside this",
        "directory so that NoteDiscovery runs without contacting a CDN. Each one",
        "keeps its own licence text alongside it. NoteDiscovery itself is MIT",
        "licensed; these notices cover the vendored files only.",
        "",
        "| Library | Version | Licence | Licence text |",
        "|---------|---------|---------|--------------|",
    ]
    for name, version, license_id, _ in sorted(results):
        lines.append(f"| {name} | {version} | {license_id} | `{name}/LICENSE` |")
    lines += [
        "",
        "DOMPurify is dual-licensed Apache-2.0 OR MPL-2.0 and vis-network is",
        "dual-licensed Apache-2.0 OR MIT; both are taken here under Apache-2.0.",
        "",
        "Regenerate this directory with `python scripts/vendor_assets.py`.",
        "",
    ]
    (dest / NOTICES_NAME).write_text("\n".join(lines), encoding="utf-8")


def load_json(path):
    if not path.exists():
        return {}
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return {}


def check_complete(dest):
    """True when the vendored tree matches the pinned versions, no network used.

    Kept cheap for the run.py startup path: existence and versions, not hashes.
    """
    manifest = load_json(dest / MANIFEST_NAME).get("packages", {})
    for spec in PACKAGES:
        expected = expected_lock_entry(spec)
        recorded = manifest.get(spec["name"])
        if not recorded or recorded.get("version") != expected["version"]:
            return False, f"{spec['name']} {expected['version']} not vendored"
        if sorted(recorded.get("hashes", {})) != expected["keys"]:
            return False, f"{spec['name']} vendored files do not match the manifest"
        if "files" in spec:
            for relative_path in spec["files"]:
                if not (dest / spec["name"] / relative_path).exists():
                    return False, f"missing {spec['name']}/{relative_path}"
        else:
            if not (dest / spec["name"] / ".complete").exists():
                return False, f"{spec['name']} extraction incomplete"
    return True, "all assets vendored"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dest", type=Path, default=DEFAULT_DEST,
                        help="vendor directory (default: frontend/vendor)")
    parser.add_argument("--check", action="store_true",
                        help="report whether assets are present, download nothing")
    parser.add_argument("--force", action="store_true",
                        help="re-download even if files are already present")
    parser.add_argument("--update-lock", action="store_true",
                        help="write the pinned hashes for what was downloaded")
    args = parser.parse_args()

    dest = args.dest.resolve()

    if args.check:
        complete, reason = check_complete(dest)
        log(reason)
        return 0 if complete else 1

    lock = load_json(LOCK_PATH)
    if not lock and not args.update_lock:
        log("No vendor_lock.json found — downloads cannot be hash-verified.")
        log("Run with --update-lock to pin the hashes of what you download.")

    dest.mkdir(parents=True, exist_ok=True)
    results = []
    for spec in PACKAGES:
        name = spec["name"]
        log(f"{name} {spec['version']} ({spec['license']})")
        package_dir = dest / name
        lock_entry = lock.get(name, {})
        if lock_entry.get("version") != spec["version"]:
            # Stale hashes: a version bump must not fail as if it were tampering
            lock_entry = {}
        if "npm" in spec:
            hashes = vendor_npm(spec, package_dir, lock_entry, args.force)
        else:
            hashes = vendor_direct(spec, package_dir, lock_entry, args.force)

        if spec.get("license_url") and not (package_dir / "LICENSE").exists():
            write_file(package_dir / "LICENSE", fetch(spec["license_url"]))
        if not (package_dir / "LICENSE").exists():
            raise RuntimeError(f"No licence text vendored for {name}.")

        results.append((name, spec["version"], spec["license"], hashes))

    (dest / MANIFEST_NAME).write_text(
        json.dumps(build_manifest(results), indent=2) + "\n", encoding="utf-8"
    )
    write_notices(dest, results)

    if args.update_lock:
        LOCK_PATH.write_text(
            json.dumps(
                {
                    name: {"version": version, "hashes": hashes}
                    for name, version, _, hashes in sorted(results)
                },
                indent=2,
            ) + "\n",
            encoding="utf-8",
        )
        log(f"Wrote pinned hashes to {LOCK_PATH.relative_to(REPO_ROOT)}")

    total = sum(f.stat().st_size for f in dest.rglob("*") if f.is_file())
    log(f"Done: {len(results)} libraries, {total / 1024 / 1024:.1f} MB in {dest}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except RuntimeError as error:
        print(f"[vendor] ERROR: {error}", file=sys.stderr)
        sys.exit(1)
