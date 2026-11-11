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

export function highlightMarkdown(source) {
    const escaped = source.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
    return escaped
        .replace(/^(#{1,6}\s.*)$/gm, '<span class="md-heading">$1</span>')
        .replace(/^(\s*(?:[-+*]|\d+[.)])\s+)/gm, '<span class="md-list">$1</span>')
        .replace(/(```[\s\S]*?```|`[^`\n]+`)/g, '<span class="md-code">$1</span>')
        .replace(/(\*\*[^*\n]+\*\*|__[^_\n]+__)/g, '<span class="md-strong">$1</span>')
        .replace(/(\*[^*\n]+\*|_[^_\n]+_)/g, '<span class="md-emphasis">$1</span>');
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
