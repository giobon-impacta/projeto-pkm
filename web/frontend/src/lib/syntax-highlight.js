function escapeHTML(source) {
    return source.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}

export function highlightCode(source, language = '') {
    const highlighter = globalThis.hljs;
    const normalizedLanguage = language.trim().toLowerCase();
    if (!highlighter || !normalizedLanguage || !highlighter.getLanguage(normalizedLanguage)) {
        return escapeHTML(source);
    }
    return highlighter.highlight(source, {
        language: normalizedLanguage,
        ignoreIllegals: true
    }).value;
}
