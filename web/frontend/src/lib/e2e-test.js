// End-to-end tests for complete PKM Notes flows
import test from 'node:test';
import assert from 'node:assert/strict';

// Mock fetch for API calls
let originalFetch;
let apiBase = '/api';

function mockFetch(records = {}, unauthorized = false) {
    originalFetch = globalThis.fetch;
    let callCount = 0;
    globalThis.fetch = async (url, options) => {
        callCount++;
        const method = options?.method || 'GET';
        const body = options?.body;
        let headers = {};
        if (options?.headers) {
            headers = { ...options.headers };
        }

        // Parse URL to get path
        const path = new URL(url).pathname;

        // Handle auth status
        if (path === '/api/auth/status') {
            if (unauthorized) {
                return new Response(JSON.stringify({ enabled: false, authenticated: false }), {
                    status: 200,
                    headers: { 'Content-Type': 'application/json' }
                });
            }
            return new Response(JSON.stringify({ enabled: true, authenticated: true }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' }
            });
        }

        // Handle auth login
        if (path === '/api/auth/login') {
            const password = JSON.parse(body || '{}').password || '';
            if (password === 'secret123') {
                return new Response(JSON.stringify({ success: true }), {
                    status: 200,
                    headers: { 'Set-Cookie': 'pkm-notes-session=abc123; HttpOnly; Path=/; SameSite=Strict', 'Content-Type': 'application/json' }
                });
            }
            return new Response(JSON.stringify({ error: 'Invalid password' }), {
                status: 401,
                headers: { 'Content-Type': 'application/json' }
            });
        }

        // Handle auth logout
        if (path === '/api/auth/logout') {
            return new Response(JSON.stringify({ success: true }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' }
            });
        }

        // Handle notes listing
        if (path === '/api/notes') {
            if (method === 'POST') {
                const title = JSON.parse(body || '{}').title || 'Untitled';
                const id = 7 + (records.notes?.length || 0);
                const note = { id, title, folder: '', tags: [], version: 1 };
                (records.notes = records.notes || []).push(note);
                return new Response(JSON.stringify(note), {
                    status: 201,
                    headers: { 'Content-Type': 'application/json' }
                });
            }
            // GET - list notes
            const notes = records.notes || [];
            return new Response(JSON.stringify({ items: notes, total: notes.length }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' }
            });
        }

        // Handle note by ID
        const noteIdMatch = path.match(/\/api\/notes\/(\d+)(?:\/.*)?$/);
        if (noteIdMatch) {
            const id = parseInt(noteIdMatch[1]);
            const note = records.notes?.find(n => n.id === id);

            if (!note) {
                return new Response(JSON.stringify({ error: 'Not found' }), {
                    status: 404,
                    headers: { 'Content-Type': 'application/json' }
                });
            }

            if (path === '/api/notes/' + id) {
                // GET note
                if (method === 'GET') {
                    return new Response(JSON.stringify(note), {
                        status: 200,
                        headers: { 'Content-Type': 'application/json' }
                    });
                }
                // PUT - update note
                if (method === 'PUT') {
                    const updated = { ...note, title: JSON.parse(body || '{}').title || note.title, version: (note.version || 0) + 1 };
                    return new Response(JSON.stringify(updated), {
                        status: 200,
                        headers: { 'Content-Type': 'application/json' }
                    });
                }
                // DELETE
                if (method === 'DELETE') {
                    (records.notes = records.notes || []).splice(
                        records.notes.findIndex(n => n.id === id), 1
                    );
                    return new Response(null, { status: 204 });
                }
            }

            // Handle attachments
            if (path === '/api/notes/' + id + '/attachments') {
                if (method === 'GET') {
                    return new Response(JSON.stringify([]), {
                        status: 200,
                        headers: { 'Content-Type': 'application/json' }
                    });
                }
                if (method === 'POST') {
                    return new Response(JSON.stringify({ id: 1, filename: 'test.txt', size: 1024, media_type: 'text/plain' }), {
                        status: 201,
                        headers: { 'Content-Type': 'application/json' }
                    });
                }
            }

            // Handle search
            if (path === '/api/search') {
                const q = options?.headers?.['content-type'] || '';
                return new Response(JSON.stringify({ items: [], total: 0 }), {
                    status: 200,
                    headers: { 'Content-Type': 'application/json' }
                });
            }

            // Handle folders
            if (path === '/api/folders') {
                return new Response(JSON.stringify([]), {
                    status: 200,
                    headers: { 'Content-Type': 'application/json' }
                });
            }

            // Handle tags
            if (path === '/api/tags') {
                return new Response(JSON.stringify([]), {
                    status: 200,
                    headers: { 'Content-Type': 'application/json' }
                });
            }

            // Handle preview
            if (path === '/api/preview') {
                return new Response('''<p>Test preview</p>''', {
                    status: 200,
                    headers: { 'Content-Type': 'text/html; charset=utf-8' }
                });
            }

            // Handle backup
            if (path === '/api/backup') {
                if (method === 'GET') {
                    return new Response(JSON.stringify({ notes: 0, attachments: 0 }), {
                        status: 200,
                        headers: { 'Content-Type': 'application/json' }
                    });
                }
                if (method === 'POST') {
                    return new Response(JSON.stringify({ notes: 1, attachments: 0 }), {
                        status: 200,
                        headers: { 'Content-Type': 'application/json' }
                    });
                }
            }

            return new Response('Not found', { status: 404 });
        }

        return new Response('Not found', { status: 404 });
    };
    return () => { globalThis.fetch = originalFetch; };
}

test.beforeEach(() => {
    globalThis.fetch = originalFetch;
});

test('complete flow: create note, edit, attach, navigate, restore backup', async () => {
    // Reset state
    const resetFetch = mockFetch();

    try {
        // Step 1: Create a note
        let response = await globalThis.fetch('/api/notes', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: 'Minha primeira nota', content: '# Olá Mundo' })
        });
        assert.ok(response.ok, 'Create note failed');
        const created = await response.json();
        assert.strictEqual(created.title, 'Minha primeira nota');
        const noteId = created.id;

        // Step 2: Edit the note
        response = await globalThis.fetch `/api/notes/${noteId}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: 'Nota editada', content: '# Olá Mundo\n\nSome content', version: created.version })
        };
        assert.ok(response.ok, 'Edit note failed');
        const updated = await response.json();
        assert.strictEqual(updated.title, 'Nota editada');

        // Step 3: Attach a file (simulated)
        response = await globalThis.fetch `/api/notes/${noteId}/attachments`, {
            method: 'POST',
            body: new FormData()
        });
        // Should handle 401 or succeed - we'll just check it doesn't crash
        if (response.status !== 401) {
            const attachment = await response.json();
            assert.ok(attachment.id, 'Attachment should have an ID');
        }

        // Step 4: Navigate (list notes)
        response = await globalThis.fetch('/api/notes', { method: 'GET' });
        assert.ok(response.ok, 'List notes failed');
        const listed = await response.json();
        assert.ok(listed.items.some(n => n.title === 'Nota editada'), 'Edited note should be in list');

        // Step 5: Restore backup (simulated - create a backup and restore)
        // First, export current state
        response = await globalThis.fetch('/api/backup', { method: 'GET' });
        assert.ok(response.ok, 'Backup export failed');
        const backupData = await response.json();

        // Restore the backup
        response = await globalThis.fetch '/api/backup', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(backupData)
        });
        assert.ok(response.ok, 'Backup restore failed');
        const restored = await response.json();
        assert.strictEqual(restored.notes, 1, 'Should restore 1 note');

        // Verify the note is restored
        response = await globalThis.fetch `/api/notes/${noteId}`, { method: 'GET' };
        assert.ok(response.ok, 'Get restored note failed');
        const finalNote = await response.json();
        assert.strictEqual(finalNote.title, 'Nota editada', 'Note should retain title after restore');

    } finally {
        resetFetch();
    }
});

test('navigation flow: open note, go back, switch folders', async () => {
    const resetFetch = mockFetch();

    try {
        // Create notes in different folders
        await globalThis.fetch('/api/notes', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: 'Work note', content: 'Work content', folder: 'Work' })
        });
        await globalThis.fetch('/api/notes', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: 'Personal note', content: 'Personal content' })
        });

        // List notes - should see both
        let response = await globalThis.fetch('/api/notes', { method: 'GET' });
        assert.ok(response.ok);
        const notes = (await response.json()).items;
        assert.ok(notes.some(n => n.folder === 'Work'), 'Work note should exist');

        // Navigate to Work folder notes
        response = await globalThis.fetch '/api/folder-notes?folder=Work', { method: 'GET' };
        assert.ok(response.ok);
        const folderNotes = (await response.json()).items;
        assert.ok(folderNotes.some(n => n.title === 'Work note'), 'Should find work note in folder');

    } finally {
        resetFetch();
    }
});

test('wikilink and mention flow', async () => {
    const resetFetch = mockFetch();

    try {
        // Create a note with wikilink references
        await globalThis.fetch('/api/notes', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: 'Note A', content: 'Check [[Note B]] for more info' })
        });

        // Create Note B
        await globalThis.fetch('/api/notes', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: 'Note B', content: 'Related content' })
        });

        // Check backlinks
        let response = await globalThis.fetch '/api/notes/1/backlinks', { method: 'GET' };
        assert.ok(response.ok, 'Get backlinks failed');
        const backlinks = await response.json();
        assert.ok(Array.isArray(backlinks), 'Backlinks should be an array');

    } finally {
        resetFetch();
    }
});