# 🔗 Public Sharing

Share notes publicly without requiring viewers to log in.

## How It Works

1. Open a note you want to share
2. Click the **Share** button in the toolbar
3. Click **Create Share Link**
4. Copy the generated URL or click **Show QR Code** for easy mobile scanning

The recipient can view the note in their browser - no account needed.

## Custom Link Names

By default the link ends in a random string, like `/share/LRFEo86oSVeJ3Gju`. To pick
the ending yourself, untick **Use a random link** before creating the link. The field
starts from the note's first few words, and the full URL is shown underneath as you
type. NoteDiscovery checks the name while you type and refuses one that another link
already uses.

Names may contain letters, numbers, hyphens and underscores, between 3 and 64
characters. Two links cannot differ only in capitalisation. Accented letters are
spelled out with their closest ASCII equivalent, so `Łódź` becomes `lodz` and
`straße` becomes `strasse` - whether you type the name or accept the suggested one.
Scripts with no Latin reading, such as Japanese or Cyrillic, cannot be used in a link
name; those notes fall back to a suggestion based on the filename.

> **A readable name is a guessable name.** A random link is effectively impossible to
> find, while `/share/recipes` is something anyone can try. Keep the random link for
> anything you would not want a stranger to stumble onto.

## Public Share Origin

Share links normally use the host you are browsing on. If you open NoteDiscovery on
your LAN (`http://192.168.x.x:8000`) but want copied / QR links to point at a public
hostname, set an optional public origin:

```yaml
# config.yaml
server:
  share_public_origin: "https://notes.example.com"
```

Or via environment variable:

```bash
SHARE_PUBLIC_ORIGIN=https://notes.example.com
```

When set, the share dialog, QR code, clipboard copy, and the API `url` field use that
origin. Leave it empty to keep the default. This only rewrites the **displayed /
returned share URL** — it does not change how the app itself is routed. Your reverse
proxy or tunnel must still make `/share/...` reachable on that host.

## Renaming a Link

Edit the name in the Share modal and click **Update Link**. A note has one share link,
so renaming moves it: **the previous URL stops working immediately.** Anyone still
holding the old link gets a "not found" page, and previously generated QR codes stop
resolving. The theme and the sharing date are kept.

## Revoking Access

To stop sharing a note:
1. Open the note
2. Click the **Share** button
3. Click **Revoke Link**

The old URL will immediately stop working.

## Features

- **Theme preserved** - Shared notes display with the theme active when you created the link
- **Images embedded** - All images are included in the shared view
- **Code highlighting** - Syntax highlighting works in shared notes
- **Copy button** - Code blocks have a copy-to-clipboard button
- **MathJax & Mermaid** - Math equations and diagrams render correctly
- **QR code** - Generate a QR code for easy mobile sharing
- **No expiration** - Links work until you revoke them

## Visual Indicators

- A **share icon** appears next to shared notes in the sidebar
- The Share modal shows the current sharing status

## Technical Details

### Token Storage

Share tokens are stored in `.share-tokens.json` in your data folder:

```json
{
  "LRFEo86oSVeJ3Gju": {
    "path": "folder/note.md",
    "theme": "dracula",
    "created": "2026-01-15T10:30:00+00:00"
  }
}
```

Each note can have one share token. Creating a new link for an already-shared note returns the existing token, unless a custom name is supplied - then the entry moves to the new token in a single write, so the note is never shared twice or not at all.

### Security

- Generated tokens are random 16-character strings
- Only the exact token URL grants access
- Revoking deletes the token permanently
- Shared notes are read-only (viewers cannot edit)
- A custom name is only as private as it is hard to guess, so it is opt-in