# 📋 Note Templates

Create notes from reusable templates with dynamic placeholder replacement.

## Overview

Templates allow you to quickly create notes with predefined structures and content. Perfect for recurring note types like meeting notes, daily journals, project plans, and more.

## How to Use Templates

### 1. Create Template Files

Templates are stored in the `data/_templates/` folder as markdown files. You will need to create such folder if it doesn't exist:

```
data/
└── _templates/
    ├── meeting-notes.md
    ├── daily-journal.md
    └── project-plan.md
```

### 2. Access Templates

Click the **"New"** button (or **+** on any folder) and select **"New from Template"**:

1. Choose a template from the dropdown
2. Enter a name for your new note
3. Click "Create Note"

The template will be copied with all placeholders replaced automatically!

### 3. Use Placeholders

Templates support dynamic placeholders that are replaced when you create a note:

| Placeholder | Description | Example |
|------------|-------------|---------|
| `{{date}}` | Current date | `2025-11-26` |
| `{{time}}` | Current time | `14:30:45` |
| `{{datetime}}` | Current date and time | `2025-11-26 14:30:45` |
| `{{timestamp}}` | Unix timestamp | `1732632645` |
| `{{year}}` | Current year | `2025` |
| `{{month}}` | Current month | `11` |
| `{{day}}` | Current day | `26` |
| `{{title}}` | Note name (without .md) | `Weekly Meeting` |
| `{{folder}}` | Parent folder name | `Projects` |

### Custom date/time formats

Need a different format than the defaults above? Any of the three
date/time placeholders — `{{date}}`, `{{time}}`, `{{datetime}}` — accepts
an optional `:FORMAT` suffix to override its default format. `FORMAT` is
any [Python `strftime()` format string](https://docs.python.org/3/library/datetime.html#strftime-and-strptime-format-codes),
so anything `strftime` can do, your template can do.

> **One rule to remember:** pick the prefix whose default already covers
> the components you want to format. Use `{{date:…}}` for date-only
> formats, `{{time:…}}` for time-only formats, and `{{datetime:…}}` when
> you need both.

#### Common recipes

| Template | Result | Use case |
|---|---|---|
| `{{date:%Y%m%d}}` | `20251126` | Date stamp for filenames |
| `{{time:%H%M%S}}` | `143045` | Time stamp for filenames |
| `{{datetime:%Y%m%d%H%M%S}}` | `20251126143045` | Sortable, filename-safe full stamp |
| `{{datetime:%Y%m%dT%H%M%S}}` | `20251126T143045` | ISO-8601 basic, no separators |
| `{{datetime:%Y-%m-%dT%H:%M:%S}}` | `2025-11-26T14:30:45` | ISO-8601 extended |
| `{{date:%d/%m/%Y}}` | `26/11/2025` | European-style date |
| `{{date:%m/%d/%Y}}` | `11/26/2025` | US-style date |
| `{{date:%A}}` | `Wednesday` | Full weekday name |
| `{{date:%a}}` | `Wed` | Short weekday name |
| `{{date:%B}}` | `November` | Full month name |
| `{{date:%V}}` | `48` | ISO week number |
| `{{date:%j}}` | `330` | Day of year (1–366) |

> Invalid format strings (typos) are left in the output unchanged so the
> mistake is visible rather than silently swallowed.

### Example Template

```markdown
---
tags: [meeting]
date: {{date}}
---

# Meeting Notes - {{title}}

**Date:** {{datetime}}  
**Participants:** 
- 

## Agenda
- 

## Discussion


## Action Items
- [ ] 

## Next Steps


```

When you create a note called "Team Sync" from this template, it becomes:

```markdown
---
tags: [meeting]
date: 2025-11-26
---

# Meeting Notes - Team Sync

**Date:** 2025-11-26 14:30:45  
**Participants:** 
- 

## Agenda
- 

## Discussion


## Action Items
- [ ] 

## Next Steps


```

## Example Templates

We provide three example templates in `documentation/templates/` that you can copy to your `data/_templates/` folder:

1. **meeting-notes.md** - Structured meeting notes with agenda, discussion, and action items
2. **daily-journal.md** - Daily journal with morning goals and evening reflection
3. **project-plan.md** - Project planning template with objectives, timeline, and status tracking

### Using Example Templates

**Option 1: Copy Manually**
```bash
cp documentation/templates/*.md data/_templates/
```

**Option 2: Create Your Own**
1. Create a `.md` file in `data/_templates/`
2. Add content and placeholders
3. Save and it's ready to use!

## Tips

- ✅ Templates can include YAML frontmatter for tags and metadata
- ✅ Use descriptive template names (they appear in the dropdown)
- ✅ Templates work in any folder - the context is preserved
- ✅ You can edit templates anytime - changes apply to new notes only
- ✅ Combine templates with tags for powerful organization

---

**See also:**
- [Tags Documentation](TAGS.md) - Learn about organizing with tags
- [Features Overview](FEATURES.md) - All application features

