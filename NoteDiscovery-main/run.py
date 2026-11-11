#!/usr/bin/env python3
"""
Quick start script for NoteDiscovery
Run this to start the application without Docker
"""

import sys
import os
import subprocess
from pathlib import Path

try:
    import colorama
    colorama.just_fix_windows_console()
except ImportError:
    colorama = None

def get_port():
    """Get port from: 1) PORT env var, 2) config.yaml, 3) default 8000."""
    if os.getenv("PORT"):
        return os.getenv("PORT")
    config_path = Path("config.yaml")
    if config_path.exists():
        try:
            import yaml
            with open(config_path, 'r', encoding='utf-8') as f:
                cfg = yaml.safe_load(f) or {}
                return str(cfg.get('server', {}).get('port', 8000))
        except Exception:
            pass
    return "8000"


def ensure_frontend_assets():
    """Download frontend/vendor/ on first start, so `python run.py` just works.

    Not fatal on failure: only the web UI needs these, the API and MCP do not.
    """
    script = Path(__file__).parent / "scripts" / "vendor_assets.py"
    if not script.exists():
        return

    already_present = subprocess.call(
        [sys.executable, str(script), "--check"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    ) == 0
    if already_present:
        return

    # flush: the subprocess writes to the same stdout right after this
    print("Downloading frontend libraries...", flush=True)
    if subprocess.call([sys.executable, str(script)]) != 0:
        print("WARNING: frontend libraries could not be downloaded.")
        print("         The web UI will not load until you run:")
        print("         python scripts/vendor_assets.py")


def main():
    try:
        import fastapi  # noqa: F401
        import uvicorn  # noqa: F401
    except ImportError:
        print("Installing dependencies...")
        subprocess.check_call([sys.executable, "-m", "pip", "install", "-r", "requirements.txt"])

    ensure_frontend_assets()

    port = get_port()
    print(f"📝 NoteDiscovery → http://localhost:{port}  (Ctrl+C to stop)")
    print()

    subprocess.call([
        sys.executable, "-m", "uvicorn",
        "backend.main:app",
        "--reload",
        "--host", "0.0.0.0",
        "--port", port,
        "--timeout-graceful-shutdown", "2"
    ])

if __name__ == "__main__":
    main()

