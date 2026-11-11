"""
Theme management for NoteDiscovery
"""

import logging
from pathlib import Path
from typing import List, Dict
import re

logger = logging.getLogger("uvicorn.error")


def parse_theme_metadata(theme_path: Path) -> Dict[str, str]:
    """Parse theme metadata from CSS file comments"""
    metadata = {
        "type": "dark"  # Default to dark for backward compatibility
    }
    
    try:
        with open(theme_path, 'r', encoding='utf-8') as f:
            # Read first few lines to find metadata
            for i, line in enumerate(f):
                if i > 10:  # Only check first 10 lines
                    break
                
                # Look for @theme-type metadata
                if '@theme-type:' in line:
                    # Extract the value (light or dark)
                    match = re.search(r'@theme-type:\s*(light|dark)', line)
                    if match:
                        metadata["type"] = match.group(1)
                        break
    except Exception as e:
        logger.error("Error parsing theme metadata from %s: %s", theme_path, e)
    
    return metadata


def get_available_themes(themes_dir: str) -> List[Dict[str, str]]:
    """Get all available themes from the themes directory"""
    themes_path = Path(themes_dir)
    themes = []
    
    # Theme icons/emojis mapping
    theme_icons = {
        "light": "🌞",
        "dark": "🌙",
        "dracula": "🧛",
        "nord": "❄️",
        "monokai": "🎞️",
        "vue-high-contrast": "💚",
        "cobalt2": "🌊",
        "vs-blue": "🔷",
        "gruvbox-dark": "🟫",
        "matcha-light": "🍵",
        "hacker": "🖥️",
        "ubuntu": "🟠"
    }
    
    # Load all themes from themes folder
    if themes_path.exists():
        for theme_file in themes_path.glob("*.css"):
            theme_name = theme_file.stem.replace("-", " ").replace("_", " ").title()
            icon = theme_icons.get(theme_file.stem, "🎨")
            
            # Parse theme metadata
            metadata = parse_theme_metadata(theme_file)
            
            themes.append({
                "id": theme_file.stem,
                "name": f"{icon} {theme_name}",
                "type": metadata["type"],  # Add theme type (light/dark)
                "builtin": False
            })
    
    return themes


def get_theme_css(themes_dir: str, theme_id: str) -> str:
    """Get the CSS content for a specific theme"""
    import re
    
    # Security: Validate theme_id to prevent path traversal
    # Only allow alphanumeric, hyphens, and underscores
    if not re.match(r'^[a-zA-Z0-9_-]+$', theme_id):
        return ""
    
    themes_path = Path(themes_dir)
    theme_path = themes_path / f"{theme_id}.css"
    
    # Security: Ensure resolved path is still within themes directory
    if not theme_path.resolve().is_relative_to(themes_path.resolve()):
        return ""
    
    if not theme_path.exists():
        return ""
    
    with open(theme_path, 'r', encoding='utf-8') as f:
        return f.read()

