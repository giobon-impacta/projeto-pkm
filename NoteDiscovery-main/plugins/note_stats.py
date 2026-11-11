"""
Note Statistics Plugin for NoteDiscovery

Computes per-note metrics (words, sentences, reading time, links, tasks, …)
served from /api/plugins/note_stats/calculate for API and MCP consumers. The
web UI computes the same metrics client-side and does not call this endpoint.
On save we also emit a one-line INFO summary for quick visibility in the
server logs.
"""

import logging
import math
import re

from fastapi import APIRouter

logger = logging.getLogger("uvicorn.error")

WORDS_PER_MINUTE = 200

# A task item on any marker GitHub-flavoured markdown renders a checkbox for: the
# three bullets or a number closed by "." or ")", optionally nested inside other
# markers or a blockquote. Group 1 is the state, " ", "x" or "X". Kept in step with
# TASK_ITEM_RE in frontend/app.js, which decides which boxes are clickable.
TASK_ITEM_PATTERN = re.compile(
    r'^\s*(?:(?:[-*+]|\d{1,9}[.)]) +|>\s*)*(?:[-*+]|\d{1,9}[.)]) +\[([ xX])(?=\] )'
)

# A CommonMark fence opener, indented ones included: a fence nested in a list item
# hides tasks just as well as one at the margin.
FENCE_PATTERN = re.compile(r'^\s*(`{3,}|~{3,})')


class Plugin:
    def __init__(self):
        self.name = "Note Statistics"
        self.version = "1.0.0"
        self.enabled = True

    def setup(self, ctx):
        """Swap the module logger for the per-plugin one the host provides."""
        global logger
        logger = ctx.logger

    def get_routes(self) -> APIRouter:
        """Expose /api/plugins/note_stats/calculate."""
        router = APIRouter()

        @router.get("/calculate")
        async def calculate(content: str):
            if not self.enabled:
                return {"enabled": False, "stats": None}
            return {"enabled": True, "stats": self.calculate_stats(content)}

        return router

    def calculate_stats(self, content: str) -> dict:
        """Compute the full metric set returned to the frontend / API."""
        words = len(re.findall(r'\S+', content))
        chars = len(re.sub(r'\s', '', content))
        total_chars = len(content)
        # floor(x + 0.5) rather than round(): round() is banker's rounding, so
        # exactly 500 words would report 2 minutes here and 3 in the browser.
        reading_time = max(1, math.floor(words / WORDS_PER_MINUTE + 0.5))
        lines = len(content.split('\n'))
        paragraphs = len([p for p in content.split('\n\n') if p.strip()])
        sentences = len(re.findall(r'[.!?]+(?:\s|$)', content))

        # Bullet/numbered list items, excluding task checkboxes like "- [ ]".
        list_items = len(re.findall(r'^\s*(?:[-*+]|\d+\.)\s+(?!\[)', content, re.MULTILINE))
        # Markdown table separator rows: | --- | :--: |
        tables = len(re.findall(r'^\s*\|(?:\s*:?-+:?\s*\|){1,}\s*$', content, re.MULTILINE))

        markdown_links = len(re.findall(r'\[([^\]]+)\]\(([^\)]+)\)', content))
        # A trailing #anchor still points at a local note, so it counts as internal.
        markdown_internal_links = len(re.findall(r'\[[^\]]+\]\([^\)]+\.md(?:#[^\)]*)?\)', content))
        wikilinks = len(re.findall(r'\[\[([^\]|]+)(?:\|[^\]]+)?\]\]', content))
        links = markdown_links + wikilinks
        internal_links = markdown_internal_links + wikilinks  # wikilinks are always internal

        code_blocks = len(re.findall(r'```[\s\S]*?```', content))
        # Fences come out first: the ``` pairs around a block otherwise match
        # the inline pattern and each block counts as an inline span as well.
        inline_code = len(re.findall(r'`[^`]+`', re.sub(r'```[\s\S]*?```', '', content)))

        h1_count = len(re.findall(r'^# ', content, re.MULTILINE))
        h2_count = len(re.findall(r'^## ', content, re.MULTILINE))
        h3_count = len(re.findall(r'^### ', content, re.MULTILINE))

        total_tasks, completed_tasks = self._count_tasks(content)

        images = len(re.findall(r'!\[([^\]]*)\]\(([^\)]+)\)', content))
        blockquotes = len(re.findall(r'^> ', content, re.MULTILINE))

        return {
            'words': words,
            'sentences': sentences,
            'characters': chars,
            'total_characters': total_chars,
            'reading_time_minutes': reading_time,
            'lines': lines,
            'paragraphs': paragraphs,
            'list_items': list_items,
            'tables': tables,
            'links': links,
            'internal_links': internal_links,
            'external_links': links - internal_links,
            'wikilinks': wikilinks,
            'code_blocks': code_blocks,
            'inline_code': inline_code,
            'headings': {
                'h1': h1_count,
                'h2': h2_count,
                'h3': h3_count,
                'total': h1_count + h2_count + h3_count,
            },
            'tasks': {
                'total': total_tasks,
                'completed': completed_tasks,
                'pending': total_tasks - completed_tasks,
                'completion_rate': round(completed_tasks / total_tasks * 100) if total_tasks else 0,
            },
            'images': images,
            'blockquotes': blockquotes,
        }

    def _count_tasks(self, content: str) -> tuple[int, int]:
        """Task items and how many are done, as the browser counts them.

        Frontmatter and fenced code are skipped: neither renders a checkbox, so
        list-shaped metadata and the "- [ ]" in a markdown example are not work
        anyone owes. Counting both states in one pass keeps them on the same rule,
        so pending can never come out negative.
        """
        lines = content.split('\n')
        start = 0

        # Frontmatter counts only when the first line opens it and a closing ---
        # follows, matching core's tag parser: an unterminated block is body text.
        if lines and lines[0].strip() == '---':
            for i in range(1, len(lines)):
                if lines[i].strip() == '---':
                    start = i + 1
                    break

        total = 0
        completed = 0
        open_fence = None

        for line in lines[start:]:
            fence = FENCE_PATTERN.match(line)
            if open_fence:
                # A fence closes on the same character, repeated at least as often.
                if fence and fence.group(1)[0] == open_fence[0] and len(fence.group(1)) >= len(open_fence):
                    open_fence = None
                continue
            if fence:
                open_fence = fence.group(1)
                continue

            task = TASK_ITEM_PATTERN.match(line)
            if task:
                total += 1
                if task.group(1) != ' ':
                    completed += 1

        return total, completed

    def on_note_save(self, note_path: str, content: str) -> str | None:
        """Emit a one-line summary on save. Doesn't modify content."""
        s = self.calculate_stats(content)
        parts = [
            f"{s['words']:,} words",
            f"{s['sentences']:,} sentences",
            f"~{s['reading_time_minutes']}m read",
            f"{s['lines']:,} lines",
        ]
        if s['list_items']:
            parts.append(f"{s['list_items']:,} lists")
        if s['tables']:
            parts.append(f"{s['tables']:,} tables")
        if s['links']:
            parts.append(f"{s['links']} links ({s['internal_links']} internal)")
        if s['tasks']['total']:
            parts.append(f"{s['tasks']['completed']}/{s['tasks']['total']} tasks")
        logger.info("note_stats %s | %s", note_path, " | ".join(parts))
        return None
