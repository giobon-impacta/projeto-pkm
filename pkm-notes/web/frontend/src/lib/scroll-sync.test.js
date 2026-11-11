import test from 'node:test';
import assert from 'node:assert/strict';
import { mapEditorToPreview, mapPreviewToEditor } from './scroll-sync.js';

test('scroll mapping uses proportional progress when the document has no landmarks', () => {
    assert.equal(mapEditorToPreview(250, 1000, 500), 125);
    assert.equal(mapPreviewToEditor(125, 500, 1000), 250);
});

test('scroll mapping aligns source landmarks across differently sized blocks', () => {
    const landmarks = [
        { editorPct: 0.25, previewTop: 100 },
        { editorPct: 0.75, previewTop: 900 }
    ];
    assert.equal(mapEditorToPreview(500, 1000, 1200, landmarks), 500);
    assert.equal(mapPreviewToEditor(500, 1200, 1000, landmarks), 500);
});

test('scroll mapping always aligns the beginning and end of both panes', () => {
    const landmarks = [{ editorPct: 0.5, previewTop: 400 }];
    assert.equal(mapEditorToPreview(0, 1000, 800, landmarks), 0);
    assert.equal(mapEditorToPreview(1000, 1000, 800, landmarks), 800);
    assert.equal(mapPreviewToEditor(0, 800, 1000, landmarks), 0);
    assert.equal(mapPreviewToEditor(800, 800, 1000, landmarks), 1000);
});
