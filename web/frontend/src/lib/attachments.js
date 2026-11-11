// Attachments module - attachment handling, upload, and UI
import { requestJSON } from './api-client.js';

export const attachmentsModule = {
    // Initialize attachments functionality
    init() {
        this.attachAttachmentButtons();
        this.attachDragDrop();
    },

    // Attach attachment button listeners
    attachAttachmentButtons() {
        const attachmentsButton = document.querySelector('#attachmentsButton');
        const attachmentInput = document.querySelector('#attachmentInput');
        const attachmentsDialog = document.querySelector('#attachmentsDialog');

        if (attachmentsButton) {
            attachmentsButton.addEventListener('click', () => {
                if (state.noteID !== null) void this.loadAttachments(state.noteID);
                if (attachmentsDialog) attachmentsDialog.showModal();
            });
        }

        if (attachmentInput) {
            attachmentInput.addEventListener('change', event => {
                const files = [...event.target.files];
                event.target.value = '';
                if (files.length) void this.uploadAttachments(files);
            });
        }

        // Close attachments dialog clicks
        const closeBtn = document.querySelector('#closeAttachments');
        if (closeBtn) {
            closeBtn.addEventListener('click', () => attachmentsDialog?.close());
        }
        attachmentsDialog?.addEventListener('click', event => {
            if (event.target === attachmentsDialog) attachmentsDialog.close();
        });
    },

    // Load attachments for a note
    async loadAttachments(noteID) {
        try {
            const attachments = await requestJSON(`/api/notes/${noteID}/attachments`);
            if (state.noteID === noteID) this.renderAttachments(attachments, noteID);
        } catch (error) {
            if (state.noteID === noteID) this.showMessage(`Não foi possível carregar os anexos: ${error.message}`, 'error');
        }
    },

    // Render attachments UI
    renderAttachments(attachments, noteID) {
        const list = document.querySelector('#attachmentList');
        const empty = document.querySelector('#attachmentsEmpty');
        const button = document.querySelector('#attachmentsButton');

        if (!list || !empty || !button) return;

        list.replaceChildren();
        for (const attachment of attachments) {
            const item = document.createElement('li');
            const link = document.createElement('a');
            link.href = attachment.url;
            link.textContent = `${attachment.filename} (${this.formatFileSize(attachment.size)})`;
            link.title = attachment.filename;
            const remove = document.createElement('button');
            remove.className = 'attachment-remove';
            remove.type = 'button';
            remove.dataset.deleteAttachment = String(attachment.id);
            remove.dataset.noteId = String(noteID);
            remove.setAttribute('aria-label', `Excluir anexo ${attachment.filename}`);
            remove.textContent = '×';
            item.append(link, remove);
            list.append(item);
        }
        list.hidden = attachments.length === 0;
        empty.hidden = attachments.length !== 0;
        const label = attachments.length ? `Anexos (${attachments.length})` : 'Abrir anexos';
        button.title = label;
        button.setAttribute('aria-label', label);
    },

    formatFileSize(size) {
        if (size < 1024) return `${size} B`;
        if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
        return `${(size / (1024 * 1024)).toFixed(1)} MB`;
    },

    // Upload attachments
    async uploadAttachments(files) {
        if (!(await flushSave()) || state.noteID === null) return;
        const noteID = state.noteID;

        for (const file of files) {
            const form = new FormData();
            form.append('file', file, file.name);
            try {
                const response = await fetch(`/api/notes/${noteID}/attachments`, { method: 'POST', body: form });
                if (response.status === 401) {
                    location.replace('/login.html');
                    return;
                }
                if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
                const attachment = await response.json();
                if (state.noteID === noteID) {
                    this.insertAttachmentMarkdown(attachment);
                    await this.loadAttachments(noteID);
                }
            } catch (error) {
                this.showMessage(`Não foi possível enviar ${file.name}: ${error.message}`, 'error');
                return;
            }
        }
        if (state.noteID === noteID) await flushSave();
    },

    insertAttachmentMarkdown(attachment) {
        const textarea = document.querySelector('#noteContent');
        if (!textarea) return;
        const label = attachment.filename.replace(/[\r\n]/g, ' ').replaceAll('\\', '\\\\').replaceAll('[', '\\[').replaceAll(']', '\\]');
        const inlineImage = ['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/avif'].includes(attachment.media_type);
        const destination = `${attachment.url}${inlineImage ? '?inline=1' : ''}`;
        const markdown = inlineImage ? `![${label}](${destination})` : `[${label}](${destination})`;
        const start = textarea.selectionStart;
        const end = textarea.selectionEnd;
        const before = textarea.value.slice(0, start);
        const after = textarea.value.slice(end);
        const prefix = before && !/\s$/.test(before) ? '\n' : '';
        const suffix = after && !/^\s/.test(after) ? '\n' : '';
        const next = `${before}${prefix}${markdown}${suffix}${after}`;
        const cursor = before.length + prefix.length + markdown.length + suffix.length;
        textarea.value = next;
        textarea.focus();
        textarea.setSelectionRange(cursor, cursor);
        markdownEditor.handleInput();
    },

    // Delete attachment
    async deleteAttachment(attachmentID) {
        const noteID = state.noteID;
        if (noteID === null) return;
        const button = document.querySelector(`[data-delete-attachment="${attachmentID}"]`);
        const filename = button?.previousElementSibling?.textContent || 'este anexo';
        if (!window.confirm(`Excluir ${filename}? Essa ação não pode ser desfeita.`)) return;
        try {
            await requestJSON(`/api/notes/${noteID}/attachments/${attachmentID}`, { method: 'DELETE' });
            await this.loadAttachments(noteID);
            this.showMessage('Anexo excluído.', 'success');
        } catch (error) {
            this.showMessage(`Não foi possível excluir o anexo: ${error.message}`, 'error');
        }
    },

    showMessage(message, type) {
        const el = document.querySelector('#appMessage');
        if (el) {
            el.textContent = message;
            el.dataset.state = type;
            el.className = `app-message is-${type}`;
        }
    }
};