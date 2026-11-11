"""
Open Task Search
Turns the search box into a vault-wide list of unchecked tasks.

Trigger: search for "@task" or "@tasks" (case-insensitive)
Install: cp plugins/contrib/search_open_tasks.py plugins/
Author:  @LuBeDa
Caveats: One result per note, not per task — the sidebar keys results by path,
         so a note shows its first open task there and carries the rest in
         `matches` for API and MCP consumers. Numbered checkboxes are also found.
         Opening a result highlights nothing, because "@tasks" is not text 
         that appears in the note. YAML frontmatter and fenced code blocks are
         skipped, so list-shaped metadata and checkbox examples inside fences
         are not read as tasks.
         Every "@tasks" search reads every note in the vault, because a task
         list has no index behind it the way core's full-text search does. On
         a large vault expect the query to take about as long as one cold
         full-text search, and to get slower as the vault grows.
"""

import logging
import os
import re
from html import escape
from pathlib import Path

logger = logging.getLogger("uvicorn.error")

# Queries that hand the result set over to this plugin. Compared against the
# stripped, lowercased query, so "@Tasks " triggers too.
TRIGGERS = {"@task", "@tasks"}

# Unchecked checkboxes only, on any marker that starts a markdown list item:
# the three bullets, or an ordered number closed by "." or ")". CommonMark caps
# that number at nine digits. "- [x]" is a finished task and never matches.
#
# Every gap is [ \t] rather than \s, which would also match the newline and let
# an empty "- [ ]" swallow the line below it as its own text.
OPEN_TASK_PATTERN = re.compile(
    r'^[ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]+\[ \][ \t]+(\S.*?)[ \t]*$',
    re.MULTILINE,
)

# A CommonMark fence opener: up to three leading spaces, then three or more
# backticks or tildes. It closes on a line of the same character, at least as
# long, with nothing but whitespace after it — the rule the preview pipeline's
# own fence scanner uses.
FENCE_OPEN_PATTERN = re.compile(r'^ {0,3}(`{3,}|~{3,})')

# A task longer than this is cut for display. Every task still gets its own
# entry — this trims a line, it never drops one.
MAX_TASK_CHARS = 200


class Plugin:
    def __init__(self):
        self.name = "Open Task Search"
        self.version = "1.0.0"
        self.enabled = True
        # Replaced in setup(). The loader always calls it, but a hook that
        # fired first would still see a usable vault path rather than None.
        self.notes_dir = Path(".")

    def setup(self, ctx):
        """Take the vault path from the host, and the per-plugin logger."""
        global logger
        logger = ctx.logger
        self.notes_dir = ctx.notes_dir

    def on_search(self, query: str, results: list) -> list | None:
        """Replace the results with the vault's open tasks, on trigger only.

        Any other query returns None, leaving core search untouched.
        """
        if query.strip().lower() not in TRIGGERS:
            return None

        notes = self._scan_vault()
        # This hook owns the ordering once it returns a list — core's sort is
        # skipped — and pagination needs one that holds still between requests.
        notes.sort(key=lambda note: note['path'].lower())

        total = sum(len(note['matches']) for note in notes)
        logger.info("search_open_tasks '%s' | %d open tasks in %d notes", query, total, len(notes))
        return notes

    def _scan_vault(self) -> list:
        """Every note holding at least one open task, in core's result shape."""
        notes = []

        for root, dirnames, filenames in os.walk(self.notes_dir):
            # Same exclusions as the core vault scan: dot-folders and dotfiles
            # hold app state, not notes.
            dirnames[:] = [d for d in dirnames if not d.startswith('.')]
            root_path = Path(root)

            for filename in filenames:
                if filename.startswith('.') or not filename.endswith('.md'):
                    continue

                full_path = root_path / filename
                try:
                    content = full_path.read_text(encoding='utf-8')
                except Exception:
                    # An unreadable note skips itself rather than failing the
                    # whole search, the way core's own scan does.
                    continue

                matches = self._open_tasks(content)
                if not matches:
                    continue

                relative_path = full_path.relative_to(self.notes_dir)
                notes.append({
                    "name": full_path.stem,
                    "path": relative_path.as_posix(),
                    "folder": relative_path.parent.as_posix() if str(relative_path.parent) != "." else "",
                    "matches": matches,
                })

        return notes

    def _open_tasks(self, content: str) -> list:
        """One match entry per unchecked task, ordered as they appear.

        Bulleted and numbered lists both count, so "- [ ]", "1. [ ]" and
        "1) [ ]" are all open tasks. "- []" is not: the box needs the space
        GitHub-flavoured markdown puts inside it, and without one the line is
        ordinary text wearing empty brackets.
        """
        matches = []
        content = self._blank_non_task_regions(content)

        for match in OPEN_TASK_PATTERN.finditer(content):
            text = match.group(1)
            if len(text) > MAX_TASK_CHARS:
                text = text[:MAX_TASK_CHARS].rstrip() + '…'

            matches.append({
                "line_number": content.count('\n', 0, match.start()) + 1,
                # Escaped before the mark goes on, so a task containing markup
                # renders as the text someone typed.
                "context": f'☐ <mark class="search-highlight">{escape(text)}</mark>',
            })

        return matches

    def _blank_non_task_regions(self, content: str) -> str:
        """Empty out the lines no task can live on, keeping every newline.

        Frontmatter carries list-shaped metadata that is not a task, and a
        fence holds an example of one rather than one someone owes. The lines
        are blanked rather than dropped because `line_number` counts newlines
        and the sidebar uses it to jump to the task.
        """
        lines = content.split('\n')
        first_body_line = 0

        # Frontmatter counts only when the very first line is `---` and a
        # closing `---` follows — the same rule core's tag parser applies, so
        # an unterminated block leaves the whole note readable.
        if lines and lines[0].strip() == '---':
            for i in range(1, len(lines)):
                if lines[i].strip() == '---':
                    lines[:i + 1] = [''] * (i + 1)
                    first_body_line = i + 1
                    break

        fence_char = None
        fence_len = 0
        for i in range(first_body_line, len(lines)):
            line = lines[i]

            if fence_char is None:
                opener = FENCE_OPEN_PATTERN.match(line)
                if opener:
                    fence_char = opener.group(1)[0]
                    fence_len = len(opener.group(1))
                    lines[i] = ''
                continue

            # Inside a fence: blank the line either way, and let a valid
            # closer end the block. An unclosed fence runs to end of note,
            # which is what a markdown renderer does with one too.
            closer = FENCE_OPEN_PATTERN.match(line)
            lines[i] = ''
            if (closer
                    and closer.group(1)[0] == fence_char
                    and len(closer.group(1)) >= fence_len
                    and not line[closer.end():].strip()):
                fence_char = None
                fence_len = 0

        return '\n'.join(lines)
