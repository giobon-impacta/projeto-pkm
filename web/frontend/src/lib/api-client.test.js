import test from 'node:test';
import assert from 'node:assert/strict';
import { requestJSON } from './api-client.js';

test('sends JSON requests and preserves caller headers', async () => {
    const originalFetch = globalThis.fetch;
    let request;
    globalThis.fetch = async (url, options) => {
        request = { url, options };
        return new Response('{"id":7}', { status: 200 });
    };
    try {
        assert.deepEqual(await requestJSON('/api/notes', {
            method: 'POST',
            body: '{"title":"Test"}',
            headers: { 'X-Note-ID': '7' }
        }), { id: 7 });
        assert.equal(request.url, '/api/notes');
        assert.equal(request.options.headers['Content-Type'], 'application/json');
        assert.equal(request.options.headers['X-Note-ID'], '7');
    } finally {
        globalThis.fetch = originalFetch;
    }
});

test('returns null for no-content responses', async () => {
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async () => new Response(null, { status: 204 });
    try {
        assert.equal(await requestJSON('/api/notes/7'), null);
    } finally {
        globalThis.fetch = originalFetch;
    }
});

test('preserves API errors and redirects unauthorized requests', async () => {
    const originalFetch = globalThis.fetch;
    const redirects = [];
    globalThis.fetch = async () => new Response('{"error":"Login required"}', { status: 401 });
    try {
        await assert.rejects(
            requestJSON('/api/notes', {}, () => redirects.push('/login.html')),
            error => error.message === 'Login required' &&
                error.status === 401 &&
                error.body.error === 'Login required'
        );
        assert.deepEqual(redirects, ['/login.html']);
    } finally {
        globalThis.fetch = originalFetch;
    }
});
