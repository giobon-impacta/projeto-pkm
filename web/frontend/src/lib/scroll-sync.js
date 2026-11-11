function interpolate(value, anchors, inputKey, outputKey) {
    let before = anchors[0];
    let after = anchors.at(-1);
    for (let index = 1; index < anchors.length; index++) {
        if (anchors[index][inputKey] >= value) {
            before = anchors[index - 1];
            after = anchors[index];
            break;
        }
    }
    const inputSpan = after[inputKey] - before[inputKey];
    const outputSpan = after[outputKey] - before[outputKey];
    const progress = inputSpan > 0 ? Math.max(0, Math.min(1, (value - before[inputKey]) / inputSpan)) : 0;
    return before[outputKey] + progress * outputSpan;
}

export function getScrollSpacerHeight(sourceScrollable, previewContentHeight, previewViewportHeight) {
    if (sourceScrollable <= 0) return 0;
    return Math.max(0, previewViewportHeight + sourceScrollable - previewContentHeight);
}

export function mapEditorToPreview(scrollTop, editorScrollable, previewScrollable, landmarks = []) {
    if (editorScrollable <= 0 || previewScrollable <= 0) return 0;
    const anchors = [{ editorPct: 0, previewTop: 0 }];
    for (const landmark of landmarks) {
        if (!Number.isFinite(landmark.editorPct) || !Number.isFinite(landmark.previewTop) ||
            landmark.editorPct <= anchors.at(-1).editorPct ||
            landmark.previewTop <= anchors.at(-1).previewTop ||
            landmark.editorPct >= 1 || landmark.previewTop >= previewScrollable) continue;
        anchors.push(landmark);
    }
    anchors.push({ editorPct: 1, previewTop: previewScrollable });
    const editorPct = Math.max(0, Math.min(1, scrollTop / editorScrollable));
    return interpolate(editorPct, anchors, 'editorPct', 'previewTop');
}

export function mapPreviewToEditor(scrollTop, previewScrollable, editorScrollable, landmarks = []) {
    if (editorScrollable <= 0 || previewScrollable <= 0) return 0;
    const anchors = [{ editorPct: 0, previewTop: 0 }];
    for (const landmark of landmarks) {
        if (!Number.isFinite(landmark.editorPct) || !Number.isFinite(landmark.previewTop) ||
            landmark.editorPct <= anchors.at(-1).editorPct ||
            landmark.previewTop <= anchors.at(-1).previewTop ||
            landmark.editorPct >= 1 || landmark.previewTop >= previewScrollable) continue;
        anchors.push(landmark);
    }
    anchors.push({ editorPct: 1, previewTop: previewScrollable });
    const previewTop = Math.max(0, Math.min(previewScrollable, scrollTop));
    const editorPct = interpolate(previewTop, anchors, 'previewTop', 'editorPct');
    return editorPct * editorScrollable;
}
