import test from 'node:test';
import assert from 'node:assert/strict';
import { highlightCode } from './syntax-highlight.js';

test('uses Highlight.js for supported fenced code languages', () => {
    const original = globalThis.hljs;
    const calls = [];
    globalThis.hljs = {
        getLanguage(language) {
            return language === 'js';
        },
        highlight(source, options) {
            calls.push({ source, options });
            return { value: '<span class="hljs-keyword">const</span> answer = 42;' };
        }
    };

    try {
        assert.equal(
            highlightCode('const answer = 42;', 'JS'),
            '<span class="hljs-keyword">const</span> answer = 42;'
        );
        assert.deepEqual(calls, [{
            source: 'const answer = 42;',
            options: { language: 'js', ignoreIllegals: true }
        }]);
    } finally {
        if (original === undefined) delete globalThis.hljs;
        else globalThis.hljs = original;
    }
});

test('escapes code for unknown languages and when Highlight.js is unavailable', () => {
    const original = globalThis.hljs;
    globalThis.hljs = { getLanguage: () => false };
    try {
        assert.equal(highlightCode('<script>alert(1)</script>', 'unknown'), '&lt;script&gt;alert(1)&lt;/script&gt;');
    } finally {
        if (original === undefined) delete globalThis.hljs;
        else globalThis.hljs = original;
    }

    delete globalThis.hljs;
    assert.equal(highlightCode('<script>', 'js'), '&lt;script&gt;');
});
