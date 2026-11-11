# Drawing editor

NoteDiscovery includes a **built-in drawing editor** for sketching and annotating directly in the app. Drawings are stored as ordinary **PNG files** in your vault, so they work like any other image: previews, export, and backups behave the same.

## Creating a drawing

Use the **+ New** menu in the sidebar and choose **New drawing**. The app creates a file named `drawing-{timestamp}.png` next to your notes (in the folder you pick), then opens it in the drawing viewer.

## Editor overview

- **Tools** — Freehand pencil, straight line, rectangle, and ellipse; **eraser** (paints with the canvas background color); **eyedropper** to sample a color from the canvas; **paint bucket** to flood-fill a connected area with the current color; **text** to drop a typed label at the click point. Undo/redo is supported like any other stroke.
- **Color & stroke width** — Color picker and width slider appear on the same toolbar as the tools. Text size also follows the stroke-width slider.
- **Undo / redo** — Use the main toolbar buttons or **Ctrl+Z** / **Ctrl+Y** (same shortcuts as the note editor; they apply to strokes while a drawing is open).
- **Clear** — Replaces the current session with a **blank white image** and schedules a save (see [FEATURES.md](FEATURES.md) for the exact behavior and confirmation).
- **Saving** — Changes are saved automatically after you finish a stroke (debounced), and you can press **Ctrl+S** (Cmd+S on Mac) to save the PNG immediately.

## Files on disk

- Only files whose names match **`drawing-*.png`** are opened in the drawing editor; other PNGs open as normal images.
- The saved PNG’s **pixel dimensions** match the drawing pane at save time (layout and device pixel ratio), similar to a screenshot of the canvas area.

## API

To update an existing drawing from automation, use **PUT `/api/media/{path}`** with a **PNG body**; the server only allows in-place updates for `drawing-*.png` paths. See [API.md](API.md#update-drawing-png-in-place).

## See also

- [FEATURES.md](FEATURES.md) — Full feature list and keyboard shortcuts  
- [API.md](API.md) — Media endpoints  
