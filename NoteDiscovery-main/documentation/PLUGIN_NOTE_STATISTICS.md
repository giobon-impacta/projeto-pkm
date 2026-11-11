# 📊 Note Statistics Plugin

**Version:** 1.0.0  
**Status:** Enabled by default  
**File:** `plugins/note_stats.py`

---

## What It Does

Calculates and displays comprehensive statistics for every note, including word count, reading time, character count, line count, links, images, code blocks, tasks, and more. Stats appear in the bottom of the UI.

---

## Requirements

**No additional dependencies required.** Uses built-in Python libraries only.

---

## Installation

**Already enabled by default!** No configuration needed.

### To Disable/Enable via API

**Disable:**
```bash
curl -X POST http://localhost:8000/api/plugins/note_stats/toggle \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'
```

**Enable:**
```bash
curl -X POST http://localhost:8000/api/plugins/note_stats/toggle \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'
```

---

## Statistics Provided

### 📝 Content Metrics
- **Words** - Total word count
- **Characters** - With and without spaces
- **Lines** - Total lines in the note
- **Paragraphs** - Number of paragraphs
- **Sentences** - Estimated sentence count

### ⏱️ Reading Time
- Calculated at 200 words per minute
- Displayed in minutes

### 🔗 Links & References
- **Total Links** - All links (markdown + wikilinks)
- **Internal Links** - Links to other notes (`.md` files and wikilinks)
- **External Links** - HTTP/HTTPS URLs
- **Wikilinks** - `[[note]]` and `[[note|display]]` count
- **Images** - `![alt](image.png)` count

### 💻 Code Blocks
- **Inline Code** - `` `code` `` count
- **Code Blocks** - ` ```language ` ` blocks
- **Languages Used** - List of detected languages

### ✅ Tasks
- **Total Tasks** - All `- [ ]` and `- [x]` items
- **Completed** - `- [x]` checked tasks
- **Pending** - `- [ ]` unchecked tasks
- **Progress** - Percentage complete

### 📑 Structure
- **Headings** - H1, H2, H3 counts
- **Lists** - Bullet and numbered lists
- **Blockquotes** - `>` quote blocks
- **Tables** - Markdown table count

---

## How It Works

### Client-Side Calculation
Statistics are calculated **in real-time in your browser** as you type, providing instant feedback without server roundtrips.

### On Save (Hook: `on_note_save`)
When a note is saved, the plugin:
1. Parses the markdown content
2. Calculates all statistics
3. Logs key metrics to Docker logs (for monitoring)

### Hooks Used
- `on_note_save` - Logs stats to Docker after save

### Endpoint

The plugin serves its own route via `get_routes()`:

```http
GET /api/plugins/note_stats/calculate?content={markdown_content}
```

It returns `{"enabled": false, "stats": null}` while the plugin is toggled off.
The web UI does not call it — the panel is computed in the browser — so it exists
for API and MCP consumers.

---

## Viewing Statistics

### In the UI
1. Open any note
2. Look at the **bottom** of the screen
3. Click to expand/collapse the stats panel
4. Statistics update in real-time as you type

### In Server Logs
```bash
# Docker (any host OS)
docker-compose logs -f | grep "note_stats"

# Running locally (Linux / macOS)
python run.py 2>&1 | grep "note_stats"

# Running locally (Windows / PowerShell)
python run.py 2>&1 | findstr "note_stats"
```

Example output (single line, prefixed with uvicorn's `INFO:`):
```
INFO:     note_stats projects/website.md | 1,234 words | 6 sentences | ~6m read | 89 lines | 15 links (5 internal) | 8/12 tasks
```

Optional sections (`lists`, `tables`, `links`, `tasks`) only appear when their count is non-zero.

---

## Configuration

No configuration needed. The plugin works out of the box.

### Customization (Optional)

To change the reading-speed assumption used for `reading_time_minutes`,
edit the constant near the top of `note_stats.py`:

```python
WORDS_PER_MINUTE = 200  # Change to your average reading speed
```

---

## Architecture

### Client-Side (Frontend)
- Statistics are calculated in JavaScript (`app.js`)
- Updates in real-time as you type
- No API calls required for stats display
- Instant feedback, zero latency

### Server-Side (Backend)
- Plugin logs stats to Docker when notes are saved
- Uses `on_note_save` hook
- Provides monitoring and tracking
- Serves `/api/plugins/note_stats/calculate` for API and MCP consumers

---

## Use Cases

### 📝 Writing Goals
Track word count to meet daily writing targets.

### ⏱️ Content Planning
Estimate reading time for blog posts or documentation.

### ✅ Task Management
Monitor task completion progress across notes.

### 📊 Content Analysis
Understand structure and complexity of notes.

### 🔗 Link Auditing
Find orphaned notes or track reference density.

---

## Troubleshooting

**Issue:** Stats not showing in UI
- **Check:** Is plugin enabled? `curl http://localhost:8000/api/plugins`
- **Check:** Hard refresh browser (Ctrl+Shift+R)
- **Fix:** Save the note again to recalculate stats

**Issue:** Wrong statistics
- **Cause:** Complex markdown formatting
- **Fix:** Plugin uses regex parsing - very complex markdown may vary
- **Note:** Statistics are estimates, not exact counts

**Issue:** Performance on large notes
- **Note:** Plugin is optimized for notes <100KB
- **For large files:** Stats calculation may take a moment on first save

---

## Technical Details

### Calculation Methods

**Words:** Split on whitespace, excluding code blocks  
**Reading Time:** `words / 200 minutes`  
**Links:** Regex match `[text](url)` markdown links + `[[wikilinks]]`  
**Wikilinks:** Regex match `[[target]]` and `[[target|display]]` (Obsidian-style)  
**Tasks:** Match `- [ ]` and `- [x]`, case-insensitive so `- [X]` counts too  
**Code Blocks:** Match ` ```language ` ` fences  
**Headings:** Match `#`, `##`, `###` at line start  

### Performance
- **Client-side calculation** - Zero latency, no server roundtrips
- **Real-time updates** - As you type, stats refresh instantly
- **Lightweight** - Typical calculation: <10ms
- **No caching needed** - Calculated on-demand in browser