import { highlightCode } from './syntax-highlight.js';

function replaceSelection(value, start, end, before, after = before, placeholder = '') {
    const selected = value.slice(start, end) || placeholder;
    const next = `${value.slice(0, start)}${before}${selected}${after}${value.slice(end)}`;
    const selectionStart = start + before.length;
    return {
        value: next,
        start: selectionStart,
        end: selectionStart + selected.length
    };
}

function changeLines(value, start, end, transform) {
    const lineStart = value.lastIndexOf('\n', Math.max(0, start - 1)) + 1;
    const lineEndIndex = value.indexOf('\n', end);
    const lineEnd = lineEndIndex < 0 ? value.length : lineEndIndex;
    const lines = value.slice(lineStart, lineEnd).split('\n');
    let removedFromFirst = 0;
    let addedToFirst = 0;
    let lengthDelta = 0;
    const changed = lines.map((line, index) => {
        const next = transform(line);
        const delta = next.length - line.length;
        lengthDelta += delta;
        if (index === 0) {
            removedFromFirst = Math.max(0, -delta);
            addedToFirst = Math.max(0, delta);
        }
        return next;
    }).join('\n');
    const nextValue = `${value.slice(0, lineStart)}${changed}${value.slice(lineEnd)}`;
    return {
        value: nextValue,
        start: Math.max(lineStart, start - removedFromFirst) + addedToFirst,
        end: Math.max(lineStart, end + lengthDelta)
    };
}

export function indentSelection(value, start, end, outdent = false) {
    if (start === end && !outdent) {
        const next = `${value.slice(0, start)}    ${value.slice(end)}`;
        return { value: next, start: start + 4, end: start + 4 };
    }
    return changeLines(value, start, end, line => {
        if (!outdent) return `    ${line}`;
        return line.startsWith('\t') ? line.slice(1) : line.replace(/^ {1,4}/, '');
    });
}

export class MarkdownEditor {
    constructor(textarea, onChange, onHistoryChange = () => {}) {
        this.textarea = textarea;
        this.onChange = onChange;
        this.onHistoryChange = onHistoryChange;
        this.history = [textarea.value];
        this.historyIndex = 0;
        this.lastInputAt = 0;
    }

    setValue(value) {
        this.textarea.value = value;
        this.history = [value];
        this.historyIndex = 0;
        this.lastInputAt = 0;
        this.notifyHistory();
        this.onChange(value);
    }

    handleInput() {
        const value = this.textarea.value;
        if (this.history[this.historyIndex] !== value) {
            const now = Date.now();
            if (this.historyIndex < this.history.length - 1) {
                this.history = this.history.slice(0, this.historyIndex + 1);
            }
            if (now - this.lastInputAt < 500 && this.historyIndex > 0) {
                this.history[this.historyIndex] = value;
            } else {
                this.history.push(value);
                this.historyIndex++;
            }
            if (this.history.length > 200) {
                this.history.shift();
                this.historyIndex--;
            }
            this.lastInputAt = now;
        }
        this.notifyHistory();
        this.onChange(value);
    }

    getWikilinkToken() {
        const { selectionStart, selectionEnd, value } = this.textarea;
        if (selectionStart !== selectionEnd) return null;
        const before = value.slice(0, selectionStart);
        const tokenStart = before.lastIndexOf('[[');
        if (tokenStart < 0 || (tokenStart > 0 && before[tokenStart - 1] === '!')) return null;
        const query = before.slice(tokenStart + 2);
        if (query.length > 100 || /[\[\]\n|]/.test(query)) return null;
        return { query, tokenStart: tokenStart + 2, caret: selectionStart };
    }

    acceptWikilink(target) {
        const token = this.getWikilinkToken();
        if (!token || !target) return false;
        const { value } = this.textarea;
        const after = value.slice(token.caret);
        const hasClosingBrackets = after.startsWith(']]');
        const closingBrackets = hasClosingBrackets ? '' : ']]';
        const next = `${value.slice(0, token.tokenStart)}${target}${closingBrackets}${after}`;
        const caret = token.tokenStart + target.length + closingBrackets.length + (hasClosingBrackets ? 2 : 0);
        this.apply({ value: next, start: caret, end: caret });
        return true;
    }

    apply(result) {
        this.textarea.value = result.value;
        this.textarea.focus();
        this.textarea.setSelectionRange(result.start, result.end);
        this.history = this.history.slice(0, this.historyIndex + 1);
        this.history.push(result.value);
        this.historyIndex++;
        if (this.history.length > 200) {
            this.history.shift();
            this.historyIndex--;
        }
        this.lastInputAt = 0;
        this.notifyHistory();
        this.onChange(result.value);
    }

    format(format) {
        if (format === 'table') {
            this.insertTable();
            return;
        }
        const value = this.textarea.value;
        const { selectionStart: start, selectionEnd: end } = this.textarea;
        let result;
        if (['bold', 'italic', 'strike', 'code'].includes(format)) {
            const wrappers = {
                bold: ['**', '**', 'texto'],
                italic: ['*', '*', 'texto'],
                strike: ['~~', '~~', 'texto'],
                code: ['`', '`', 'código']
            }[format];
            result = replaceSelection(value, start, end, ...wrappers);
        } else if (format === 'link') {
            const selected = value.slice(start, end);
            result = replaceSelection(value, start, end, '[', '](url)', selected || 'texto');
            result.end = result.start + (selected || 'texto').length;
        } else if (format === 'codeblock') {
            result = replaceSelection(value, start, end, '```\n', '\n```', 'código');
        } else {
            const prefix = {
                heading: '## ',
                quote: '> ',
                unordered: '- ',
                ordered: '1. ',
                task: '- [ ] '
            }[format];
            if (!prefix) return;
            result = changeLines(value, start, end, line => `${prefix}${line}`);
            if (start === end) result.end = result.start;
        }
        this.apply(result);
    }

    handleKeydown(event) {
        const mod = event.ctrlKey || event.metaKey;
        if (mod && event.key.toLowerCase() === 'z') {
            event.preventDefault();
            if (event.shiftKey) this.redo();
            else this.undo();
            return;
        }
        if (mod && event.key.toLowerCase() === 'y') {
            event.preventDefault();
            this.redo();
            return;
        }
        if (mod && ['b', 'i'].includes(event.key.toLowerCase())) {
            event.preventDefault();
            this.format(event.key.toLowerCase() === 'b' ? 'bold' : 'italic');
            return;
        }
        if (mod && event.key.toLowerCase() === 'k') {
            event.preventDefault();
            this.format('link');
            return;
        }
        if (mod && event.altKey && event.key.toLowerCase() === 't') {
            event.preventDefault();
            this.insertTable();
            return;
        }
        if (event.key === 'Tab') {
            event.preventDefault();
            const { selectionStart, selectionEnd } = this.textarea;
            this.apply(indentSelection(this.textarea.value, selectionStart, selectionEnd, event.shiftKey));
            return;
        }
        if (event.key === 'Enter' && this.continueMarkdownList()) {
            event.preventDefault();
            return;
        }
        const pairs = { '(': ')', '[': ']', '{': '}', '"': '"', "'": "'" };
        const { selectionStart, selectionEnd, value } = this.textarea;
        if (pairs[event.key] && selectionStart !== selectionEnd) {
            event.preventDefault();
            this.apply(replaceSelection(value, selectionStart, selectionEnd, event.key, pairs[event.key]));
        } else if (pairs[event.key] && selectionStart === selectionEnd) {
            event.preventDefault();
            this.apply({
                value: `${value.slice(0, selectionStart)}${event.key}${pairs[event.key]}${value.slice(selectionEnd)}`,
                start: selectionStart + 1,
                end: selectionStart + 1
            });
        } else if ([')', ']', '}', '"', "'"].includes(event.key) &&
            value[selectionStart] === event.key && selectionStart === selectionEnd) {
            event.preventDefault();
            this.textarea.setSelectionRange(selectionStart + 1, selectionStart + 1);
        }
    }

    insertTable() {
        const value = this.textarea.value;
        const { selectionStart: start, selectionEnd: end } = this.textarea;
        let insertionStart = start;
        let insertionEnd = end;
        while (insertionStart > 0 && /[ \t]/.test(value[insertionStart - 1])) insertionStart--;
        while (insertionEnd < value.length && /[ \t]/.test(value[insertionEnd])) insertionEnd++;
        const table = '| Coluna 1 | Coluna 2 |\n| --- | --- |\n| Célula | Célula |';
        const before = insertionStart > 0 && value[insertionStart - 1] !== '\n'
            ? '\n\n'
            : insertionStart > 1 && value[insertionStart - 1] === '\n' && value[insertionStart - 2] !== '\n' ? '\n' : '';
        const after = insertionEnd < value.length && value[insertionEnd] !== '\n'
            ? '\n\n'
            : insertionEnd < value.length - 1 && value[insertionEnd] === '\n' && value[insertionEnd + 1] !== '\n' ? '\n' : '';
        const cellStart = table.indexOf('Célula');
        this.apply({
            value: `${value.slice(0, insertionStart)}${before}${table}${after}${value.slice(insertionEnd)}`,
            start: insertionStart + before.length + cellStart,
            end: insertionStart + before.length + cellStart + 'Célula'.length
        });
    }

    continueMarkdownList() {
        const { value, selectionStart, selectionEnd } = this.textarea;
        if (selectionStart !== selectionEnd) return false;
        const lineStart = value.lastIndexOf('\n', selectionStart - 1) + 1;
        const line = value.slice(lineStart, selectionStart);
        const match = line.match(/^([ \t]*(?:[-+*]|\d+[.)])\s+(?:\[[ xX]\]\s+)?|[ \t]*>\s?)(.*)$/);
        if (!match) return false;
        if (match[2].trim() === '') {
            this.apply({
                value: `${value.slice(0, lineStart)}${value.slice(selectionStart)}`,
                start: lineStart,
                end: lineStart
            });
        } else {
            const prefix = match[1].replace(/^(\s*)\d+([.)])/, (_, indent, punctuation) =>
                `${indent}${Number(line.match(/^\s*(\d+)/)?.[1] || 0) + 1}${punctuation}`);
            this.apply({
                value: `${value.slice(0, selectionStart)}\n${prefix}${value.slice(selectionEnd)}`,
                start: selectionStart + 1 + prefix.length,
                end: selectionStart + 1 + prefix.length
            });
        }
        return true;
    }

    undo() {
        if (this.historyIndex === 0) return;
        this.historyIndex--;
        this.restoreHistory();
    }

    redo() {
        if (this.historyIndex >= this.history.length - 1) return;
        this.historyIndex++;
        this.restoreHistory();
    }

    restoreHistory() {
        const value = this.history[this.historyIndex];
        this.textarea.value = value;
        this.textarea.focus();
        this.textarea.setSelectionRange(value.length, value.length);
        this.lastInputAt = 0;
        this.notifyHistory();
        this.onChange(value);
    }

    notifyHistory() {
        this.onHistoryChange({
            canUndo: this.historyIndex > 0,
            canRedo: this.historyIndex < this.history.length - 1
        });
    }
}

function escapeHTML(source) {
    return source.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}

function highlightMarkdownLine(source) {
    const inlineTokens = /(!?\[\[[^\]\n]+\]\]|`[^`\n]+`|\*\*[^*\n]+\*\*|__[^_\n]+__|\*[^*\n]+\*|_[^_\n]+_|\[[^\]\n]+\]\([^)]+\))/g;
    let result = '';
    let previousEnd = 0;
    for (const match of source.matchAll(inlineTokens)) {
        const [token] = match;
        const start = match.index;
        result += escapeHTML(source.slice(previousEnd, start));
        const tokenClass = token.startsWith('[[') || token.startsWith('![[')
            ? 'md-wikilink'
            : token.startsWith('`')
                ? 'md-code'
                : token.startsWith('**') || token.startsWith('__')
                    ? 'md-strong'
                    : token.startsWith('*') || token.startsWith('_')
                        ? 'md-emphasis'
                        : 'md-link';
        result += `<span class="${tokenClass}">${escapeHTML(token)}</span>`;
        previousEnd = start + token.length;
    }
    result += escapeHTML(source.slice(previousEnd));
    if (/^\s{0,3}#{1,6}\s/.test(source)) result = `<span class="md-heading">${result}</span>`;
    else if (/^\s{0,3}(?:[-+*]|\d+[.)])\s/.test(source)) result = result.replace(/^(\s*(?:[-+*]|\d+[.)])\s+)/, '<span class="md-list">$1</span>');
    else if (/^\s{0,3}>\s?/.test(source)) result = result.replace(/^(\s*&gt;\s?)/, '<span class="md-quote">$1</span>');
    return result;
}

export function highlightMarkdown(source) {
    const lines = source.split('\n');
    const highlighted = [];
    for (let index = 0; index < lines.length; index++) {
        const line = lines[index];
        const opening = line.match(/^(\s*)(`{3,}|~{3,})([^\s`]*)?(.*)$/);
        if (!opening) {
            highlighted.push(highlightMarkdownLine(line));
            continue;
        }

        const [, indent, delimiter, language = '', rest] = opening;
        const languageLabel = language
            ? `<span class="md-code-language">${escapeHTML(language)}</span>`
            : '';
        highlighted.push(`${escapeHTML(indent)}<span class="md-code-fence">${escapeHTML(delimiter)}</span>${languageLabel}${escapeHTML(rest)}`);
        const code = [];
        let closed = false;
        while (++index < lines.length) {
            const closing = lines[index].match(/^\s*(`{3,}|~{3,})\s*$/);
            if (closing && closing[1][0] === delimiter[0] && closing[1].length >= delimiter.length) {
                highlighted.push(`<span class="md-code-fence">${escapeHTML(lines[index])}</span>`);
                closed = true;
                break;
            }
            code.push(lines[index]);
        }
        if (code.length) {
            const body = highlightCode(code.join('\n'), language);
            highlighted.splice(highlighted.length - (closed ? 1 : 0), 0, ...body.split('\n'));
        }
    }
    return highlighted.join('\n');
}

export function ensureFrontmatterTags(content, tags) {
    if (!tags.length) return content;
    const lines = content.split('\n');
    const formatTag = tag => /^[\p{L}\p{N}_.\/-]+$/u.test(tag) ? tag : JSON.stringify(tag);
    const tagLines = tags.length === 1
        ? [`tags: ${formatTag(tags[0])}`]
        : ['tags:', ...tags.map(tag => `  - ${formatTag(tag)}`)];
    if (lines[0]?.trim() === '---') {
        const closeIndex = lines.findIndex((line, index) => index > 0 && ['---', '...'].includes(line.trim()));
        if (closeIndex > 0) {
            const existing = lines.slice(1, closeIndex).some(line => /^\s*tags\s*:/.test(line));
            if (existing) return content;
            lines.splice(closeIndex, 0, ...tagLines);
            return lines.join('\n');
        }
    }
    const frontmatter = ['---', ...tagLines, '---', ''];
    return [...frontmatter, ...lines].join('\n');
}

export function stripFrontmatter(content) {
    const lines = content.split('\n');
    if (lines[0]?.trim() !== '---') return content;
    const closeIndex = lines.findIndex((line, index) => index > 0 && ['---', '...'].includes(line.trim()));
    return closeIndex < 0 ? content : lines.slice(closeIndex + 1).join('\n').replace(/^\s*\n/, '');
}

export function frontmatterLineOffset(content) {
    return content.split('\n').length - stripFrontmatter(content).split('\n').length;
}

export function parseFrontmatter(content) {
    const lines = content.split('\n');
    if (lines[0]?.trim() !== '---') return null;
    const closeIndex = lines.findIndex((line, index) => index > 0 && ['---', '...'].includes(line.trim()));
    if (closeIndex < 0) return null;

    const metadata = {};
    let currentKey = null;
    let currentValue = [];
    const parseValue = raw => {
        const value = raw.trim();
        if (!value) return null;
        if (value.startsWith('[') && value.endsWith(']')) {
            return value.slice(1, -1).split(',').map(item => item.trim().replace(/^["']|["']$/g, '')).filter(Boolean);
        }
        if (/^(true|false)$/i.test(value)) return value.toLowerCase() === 'true';
        if (/^-?\d+(?:\.\d+)?$/.test(value)) return Number(value);
        if (/^(null|~)$/i.test(value)) return null;
        return value.replace(/^["']|["']$/g, '');
    };
    const saveValue = () => {
        if (currentKey === null) return;
        const listItems = currentValue
            .map(line => line.match(/^\s+-\s*(.*)$/)?.[1])
            .filter(value => value !== undefined)
            .map(parseValue)
            .filter(value => value !== null);
        metadata[currentKey] = listItems.length
            ? listItems
            : parseValue(currentValue.join('\n'));
    };

    for (const line of lines.slice(1, closeIndex)) {
        const keyValue = line.match(/^([a-zA-Z_][\w-]*):\s*(.*)$/);
        if (keyValue) {
            saveValue();
            currentKey = keyValue[1];
            currentValue = [keyValue[2]];
        } else if (currentKey && /^\s+/.test(line)) {
            currentValue.push(line);
        }
    }
    saveValue();
    return Object.keys(metadata).length ? metadata : null;
}

export function extractMarkdownOutline(content) {
    const lines = content.split('\n');
    const outline = [];
    let inFrontmatter = lines[0]?.trim() === '---';
    let inFence = null;
    for (let index = 0; index < lines.length; index++) {
        const line = lines[index];
        if (inFrontmatter) {
            if (index > 0 && ['---', '...'].includes(line.trim())) inFrontmatter = false;
            continue;
        }
        const fence = line.match(/^ {0,3}(`{3,}|~{3,})/);
        if (inFence) {
            if (fence && fence[1][0] === inFence.character && fence[1].length >= inFence.length) inFence = null;
            continue;
        }
        if (fence) {
            inFence = { character: fence[1][0], length: fence[1].length };
            continue;
        }

        const atx = line.match(/^ {0,3}(#{1,6})\s+(.+?)\s*#*\s*$/);
        if (atx) {
            outline.push({
                level: atx[1].length,
                text: atx[2].trim(),
                line: index
            });
            continue;
        }
        if (index > 0 && line.trim() && /^ {0,3}(=+|-+)\s*$/.test(line) && lines[index - 1].trim()) {
            outline.push({
                level: line.trim()[0] === '=' ? 1 : 2,
                text: lines[index - 1].trim(),
                line: index - 1
            });
        }
    }

    if (inFrontmatter) return [];
    const bodyLineOffset = frontmatterLineOffset(content);
    return outline.map(heading => ({ ...heading, bodyLine: heading.line - bodyLineOffset }));
}
