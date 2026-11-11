import test from 'node:test';
import assert from 'node:assert/strict';
import { ensureFrontmatterTags, indentSelection, MarkdownEditor, stripFrontmatter } from './markdown-editor.js';

function createTextarea(value, start = 0, end = start) {
    return {
        value,
        selectionStart: start,
        selectionEnd: end,
        focus() {},
        setSelectionRange(nextStart, nextEnd) {
            this.selectionStart = nextStart;
            this.selectionEnd = nextEnd;
        }
    };
}

test('indents selected lines and preserves the selection', () => {
    const result = indentSelection('one\ntwo\nthree', 0, 7);
    assert.equal(result.value, '    one\n    two\nthree');
    assert.equal(result.start, 4);
    assert.equal(result.end, 15);
});

test('outdents selected lines without removing unselected lines', () => {
    const result = indentSelection('    one\n    two\nthree', 4, 15, true);
    assert.equal(result.value, 'one\ntwo\nthree');
});

test('formats a selection and supports undo/redo', () => {
    const textarea = createTextarea('hello', 0, 5);
    const changes = [];
    const editor = new MarkdownEditor(textarea, value => changes.push(value));
    editor.format('bold');
    assert.equal(textarea.value, '**hello**');
    editor.undo();
    assert.equal(textarea.value, 'hello');
    editor.redo();
    assert.equal(textarea.value, '**hello**');
    assert.deepEqual(changes, ['**hello**', 'hello', '**hello**']);
});

test('continues ordered lists with the next number', () => {
    const textarea = createTextarea('1. first', 8);
    const editor = new MarkdownEditor(textarea, () => {});
    assert.equal(editor.continueMarkdownList(), true);
    assert.equal(textarea.value, '1. first\n2. ');
});

test('adds legacy tags to Markdown frontmatter without a separate tags field', () => {
    const markdown = ensureFrontmatterTags('# Note\n\nBody', ['sei-la']);
    assert.equal(markdown, '---\ntags: sei-la\n---\n\n# Note\n\nBody');
});

test('preserves existing frontmatter tags', () => {
    const markdown = '---\ntags: [existing]\n---\n\n# Note';
    assert.equal(ensureFrontmatterTags(markdown, ['legacy']), markdown);
});

test('omits metadata frontmatter from the rendered Markdown body', () => {
    assert.equal(stripFrontmatter('---\ntags: pkm\n---\n\n# Note'), '# Note');
});
