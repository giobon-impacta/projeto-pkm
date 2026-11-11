import test from 'node:test';
import assert from 'node:assert/strict';
import { ensureFrontmatterTags, extractMarkdownOutline, frontmatterLineOffset, highlightCode, highlightMarkdown, indentSelection, MarkdownEditor, parseFrontmatter, stripFrontmatter } from './markdown-editor.js';

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

test('inserts a Markdown link with Ctrl+K', () => {
    const textarea = createTextarea('selected text', 0, 13);
    const editor = new MarkdownEditor(textarea, () => {});
    let prevented = false;
    editor.handleKeydown({
        ctrlKey: true,
        metaKey: false,
        key: 'k',
        preventDefault() { prevented = true; }
    });
    assert.equal(prevented, true);
    assert.equal(textarea.value, '[selected text](url)');
});

test('detects an unfinished wikilink at the caret', () => {
    const textarea = createTextarea('before [[Tar', 12);
    const editor = new MarkdownEditor(textarea, () => {});
    assert.deepEqual(editor.getWikilinkToken(), { query: 'Tar', tokenStart: 9, caret: 12 });
    textarea.value = '![[image';
    textarea.selectionStart = textarea.selectionEnd = textarea.value.length;
    assert.equal(editor.getWikilinkToken(), null);
});

test('accepts a wikilink suggestion and preserves existing closing brackets', () => {
    const textarea = createTextarea('[[Tar]] after', 5);
    const editor = new MarkdownEditor(textarea, () => {});
    assert.equal(editor.acceptWikilink('Target'), true);
    assert.equal(textarea.value, '[[Target]] after');
    assert.equal(textarea.selectionStart, 10);
    editor.undo();
    assert.equal(textarea.value, '[[Tar]] after');
});

test('accepts a wikilink suggestion when closing brackets are not typed yet', () => {
    const textarea = createTextarea('[[Tar and more', 5);
    const editor = new MarkdownEditor(textarea, () => {});
    assert.equal(editor.acceptWikilink('Target'), true);
    assert.equal(textarea.value, '[[Target]] and more');
});

test('Ctrl+Alt+T inserts a table on its own Markdown lines', () => {
    const textarea = createTextarea('before after', 7, 7);
    const editor = new MarkdownEditor(textarea, () => {});
    let prevented = false;
    editor.handleKeydown({
        ctrlKey: true,
        metaKey: false,
        altKey: true,
        key: 't',
        preventDefault() { prevented = true; }
    });
    assert.equal(prevented, true);
    assert.equal(textarea.value, 'before\n\n| Coluna 1 | Coluna 2 |\n| --- | --- |\n| Célula | Célula |\n\nafter');
    assert.equal(textarea.value.slice(textarea.selectionStart, textarea.selectionEnd), 'Célula');
});

test('inserts and selects a Markdown table cell', () => {
    const textarea = createTextarea('', 0, 0);
    const editor = new MarkdownEditor(textarea, () => {});
    editor.insertTable();
    assert.equal(textarea.value, '| Coluna 1 | Coluna 2 |\n| --- | --- |\n| Célula | Célula |');
    assert.equal(textarea.value.slice(textarea.selectionStart, textarea.selectionEnd), 'Célula');
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

test('counts the lines removed with the frontmatter so body lines map back to the editor', () => {
    assert.equal(frontmatterLineOffset('---\ntags: pkm\n---\n\n# Note'), 4);
    assert.equal(frontmatterLineOffset('---\ntags:\n  - pkm\n  - notes\n---\n\n\nBody'), 7);
    assert.equal(frontmatterLineOffset('# Note\n\nBody'), 0);
});

test('parses scalar and list frontmatter properties for the collapsible preview', () => {
    assert.deepEqual(parseFrontmatter('---\ntitle: "Example"\ntags:\n  - notes\n  - pkm\nstatus: true\npriority: 2\n---\nBody'), {
        title: 'Example',
        tags: ['notes', 'pkm'],
        status: true,
        priority: 2
    });
});

test('does not expose malformed or absent frontmatter as properties', () => {
    assert.equal(parseFrontmatter('# Note\n---\ntags: pkm'), null);
    assert.equal(parseFrontmatter('---\ntags: pkm\n# Note'), null);
    assert.equal(parseFrontmatter('---\n---\n# Note'), null);
});

test('extracts nested headings, skips frontmatter and fenced code, and tracks preview lines', () => {
    assert.deepEqual(extractMarkdownOutline('---\ntags: pkm\n---\n\n# First\n\n## Second\n\n```md\n# Not a heading\n```\n\nThird\n---'), [
        { level: 1, text: 'First', line: 4, bodyLine: 0 },
        { level: 2, text: 'Second', line: 6, bodyLine: 2 },
        { level: 2, text: 'Third', line: 12, bodyLine: 8 }
    ]);
});

test('highlights wikilinks in the Markdown editor', () => {
    assert.equal(
        highlightMarkdown('[[Target]] <unsafe>'),
        '<span class="md-wikilink">[[Target]]</span> &lt;unsafe&gt;'
    );
});

test('highlights Markdown structures and identifies fenced code languages', () => {
    const highlighted = highlightMarkdown('# Título\n- **forte**\n```js\nconst answer = 42;\n```');
    assert.match(highlighted, /class="md-heading"/);
    assert.match(highlighted, /class="md-list"/);
    assert.match(highlighted, /class="md-strong"/);
    assert.match(highlighted, /class="md-code-language">js<\/span>/);
    assert.match(highlighted, /class="syntax-keyword">const<\/span>/);
    assert.match(highlighted, /class="syntax-number">42<\/span>/);
    assert.equal(highlightMarkdown('**forte**'), '<span class="md-strong">**forte**</span>');
});

test('highlights supported code and escapes code from unsupported languages', () => {
    assert.equal(
        highlightCode('const value = "<unsafe>";', 'js'),
        '<span class="syntax-keyword">const</span> value = <span class="syntax-string">"&lt;unsafe&gt;"</span>;'
    );
    assert.equal(highlightCode('<unsafe>', 'unknown'), '&lt;unsafe&gt;');
    assert.match(highlightCode('<button type="button">', 'html'), /class="syntax-keyword">button<\/span>/);
});
