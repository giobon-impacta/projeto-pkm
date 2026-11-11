"""
NoteDiscovery MCP Server

A Model Context Protocol (MCP) server that enables AI assistants
to interact with NoteDiscovery notes.

Usage:
    # As module
    python -m mcp_server

    # As installed CLI
    notediscovery-mcp

Environment Variables:
    NOTEDISCOVERY_URL: NoteDiscovery server URL (default: http://localhost:8000)
    NOTEDISCOVERY_API_KEY: API key for authentication (optional)
"""

from pathlib import Path


def _read_version() -> str:
    """
    Resolve the package version.

    Order:
      1. importlib.metadata — works when installed via pip/uvx/setuptools
      2. VERSION file at repo root — works when running from source / Docker
      3. fallback string — so the server still starts cleanly in odd setups
    """
    try:
        from importlib.metadata import version
        return version("notediscovery")
    except Exception:
        pass
    try:
        version_file = Path(__file__).resolve().parent.parent / "VERSION"
        if version_file.is_file():
            return version_file.read_text(encoding="utf-8").strip()
    except Exception:
        pass
    return "0.0.0+dev"


__version__ = _read_version()
__author__ = "NoteDiscovery"

from .server import main

__all__ = ["main", "__version__"]
