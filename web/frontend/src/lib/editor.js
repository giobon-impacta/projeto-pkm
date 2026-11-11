// Editor module - Markdown editor functionality, preview, saving, and UI interactions
import { MarkdownEditor } from './markdown-editor.js';
import { highlightMarkdown, ensureFrontmatterTags, stripFrontmatter, parseFrontmatter, frontmatterLineOffset, extractMarkdownOutline } from './markdown-editor.js';
import { highlightCode } from './syntax-highlight.js';
import { requestJSON } from './api-client.js';

export const editor = {
    init(textarea, onChange) {
        this.textarea = textarea;
        this.markdownEditor = new MarkdownEditor(textarea, () => {
            this.syncHighlight();
            this.syncPreviewMinHeight();
            this.updatePreview();
            this.scheduleSave();
            this.refreshOutline();
        }, updateEditorHistoryButtons);
        this.syncHighlight();
        this.setupGraphButton();
        return this.markdownEditor;
    },

    syncHighlight() {
        const highlightContainer = document.querySelector('#noteContentHighlight');
        if (!highlightContainer || !this.textarea) return;
        const content = this.textarea.value;
        highlightContainer.innerHTML = `${highlightMarkdown(content)}\n`;
        highlightContainer.scrollTop = this.textarea.scrollTop;
        highlightContainer.scrollLeft = this.textarea.scrollLeft;
    },

    syncPreviewMinHeight() {
        const preview = document.querySelector('#markdownPreview');
        if (!preview || !this.textarea || this.textarea.clientHeight === 0 || preview.clientHeight === 0) return;
        const contentEnd = preview.lastElementChild;
        const contentHeight = contentEnd
            ? Math.max(0, contentEnd.getBoundingClientRect().bottom - preview.getBoundingClientRect().top + preview.scrollTop)
            : 0;
        const neededHeight = Math.max(0, this.textarea.scrollHeight - this.textarea.clientHeight);
        // Simplified height sync - full logic would need more context
        if (neededHeight > 0) {
            let spacer = preview.querySelector(':scope > .preview-scroll-spacer');
            if (!spacer) {
                spacer = document.createElement('div');
                spacer.className = 'preview-scroll-spacer';
                preview.append(spacer);
            }
            spacer.style.height = `${neededHeight}px`;
        } else {
            const spacer = preview.querySelector(':scope > .preview-scroll-spacer');
            if (spacer) spacer.remove();
        }
    },

    updatePreview() {
        const preview = document.querySelector('#markdownPreview');
        if (!preview || !this.textarea) return;
        const markdown = stripFrontmatter(this.textarea.value);
        if (!markdown.trim()) {
            preview.textContent = '';
            this.renderFrontmatterProperties();
            return;
        }
        const requestID = (this.previewRequestID || 0) + 1;
        this.previewRequestID = requestID;
        window.clearTimeout(this.previewTimer);
        this.previewTimer = window.setTimeout(async () => {
            if (requestID !== this.previewRequestID) return;
            try {
                const response = await fetch('/api/preview', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'text/plain; charset=utf-8',
                        ...(state.noteID === null ? {} : { 'X-Note-ID': String(state.noteID) })
                    },
                    body: markdown
                });
                if (response.status === 401) {
                    // location.replace('/login.html');
                    this.showMessage('Não autenticado', 'error');
                    return;
                }
                if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
                const html = await response.text();
                preview.innerHTML = html;
                this.highlightPreviewCode();
                this.renderFrontmatterProperties();
                this.syncPreviewMinHeight();
                this.annotatePreviewLandmarks(markdown);
            } catch (error) {
                if (requestID === this.previewRequestID) {
                    preview.textContent = `Falha ao renderizar Markdown: ${error.message}`;
                    this.renderFrontmatterProperties();
                    this.syncPreviewMinHeight();
                }
            }
        }, 140);
    },

    highlightPreviewCode() {
        for (const code of document.querySelectorAll('#markdownPreview pre > code')) {
            const languageClass = [...code.classList].find(name => name.startsWith('language-'));
            const language = languageClass ? languageClass.slice('language-'.length) : '';
            const safeLanguage = /^[\w+-]{1,30}$/.test(language) ? language : '';
            const pre = code.parentElement;
            if (safeLanguage) pre.dataset.language = safeLanguage;
            else delete pre.dataset.language;
            code.innerHTML = highlightCode(code.textContent, safeLanguage);
        }
    },

    renderFrontmatterProperties() {
        const preview = document.querySelector('#markdownPreview');
        if (!preview) return;
        preview.querySelector('.frontmatter-properties')?.remove();
        const metadata = parseFrontmatter(this.textarea.value);
        if (!metadata) return;

        const panel = document.createElement('section');
        panel.className = 'frontmatter-properties';
        panel.setAttribute('aria-label', 'Propriedades da nota');

        const tagsValue = metadata.tags;
        const tags = tagsValue === undefined || tagsValue === null
            ? []
            : (Array.isArray(tagsValue) ? tagsValue : [tagsValue]).map(String);
        const priority = ['date', 'created', 'author', 'status', 'priority', 'type', 'category'];
        const priorityFields = priority
            .filter(key => metadata[key] !== undefined && metadata[key] !== null && !Array.isArray(metadata[key]))
            .slice(0, 3);

        const summary = document.createElement('button');
        summary.type = 'button';
        summary.className = 'frontmatter-summary';
        summary.dataset.frontmatterToggle = '';
        summary.setAttribute('aria-expanded', String(state.frontmatterExpanded));
        summary.setAttribute('aria-label', state.frontmatterExpanded
            ? 'Recolher propriedades da nota'
            : 'Expandir propriedades da nota');

        const summaryLabel = document.createElement('span');
        summaryLabel.className = 'frontmatter-summary-label';
        summaryLabel.textContent = '⚙ Propriedades';
        summary.append(summaryLabel);
        for (const tag of tags.slice(0, 5)) {
            const chip = document.createElement('span');
            chip.className = 'frontmatter-tag';
            chip.textContent = `#${tag}`;
            summary.append(chip);
        }
        if (tags.length > 5) {
            const more = document.createElement('span');
            more.className = 'frontmatter-muted';
            more.textContent = `+${tags.length - 5}`;
            summary.append(more);
        }
        for (const key of priorityFields) {
            const field = document.createElement('span');
            field.className = 'frontmatter-summary-field';
            field.textContent = `${key}: ${metadata[key]}`;
            summary.append(field);
        }
        const summaryChevron = document.createElement('span');
        summaryChevron.className = 'frontmatter-chevron';
        summaryChevron.textContent = '▼';
        summary.append(summaryChevron);
        if (!state.frontmatterExpanded) panel.append(summary);

        if (state.frontmatterExpanded) {
            const details = document.createElement('div');
            details.className = 'frontmatter-details';
            const header = document.createElement('button');
            header.type = 'button';
            header.className = 'frontmatter-details-header';
            header.dataset.frontmatterToggle = '';
            header.setAttribute('aria-expanded', 'true');
            header.textContent = '⚙ Propriedades · Recolher ▲';
            details.append(header);

            for (const [key, rawValue] of Object.entries(metadata)) {
                const row = document.createElement('div');
                row.className = 'frontmatter-row';
                const name = document.createElement('span');
                name.className = 'frontmatter-key';
                name.textContent = key;
                const value = document.createElement('span');
                value.className = 'frontmatter-value';
                const values = Array.isArray(rawValue) ? rawValue : [rawValue];
                value.textContent = values.map(item => item === null ? '' : String(item)).join(', ');
                row.append(name, value);
                details.append(row);
            }
            panel.append(details);
        }
        preview.prepend(panel);
    },

    annotatePreviewLandmarks(markdown) {
        const byKind = new Map();
        for (const landmark of this.scanMarkdownLandmarks(markdown)) {
            if (!byKind.has(landmark.kind)) byKind.set(landmark.kind, []);
            byKind.get(landmark.kind).push(landmark);
        }
        const previewBlocks = [...document.querySelectorAll('#markdownPreview > *)')].map(element => ({
            element,
            kind: this.previewBlockKind(element)
        }));
        const previewByKind = new Map();
        for (const block of previewBlocks) {
            if (!block.kind) continue;
            if (!previewByKind.has(block.kind)) previewByKind.set(block.kind, []);
            previewByKind.get(block.kind).push(block.element);
        }
        for (const [kind, landmarks] of byKind) {
            const blocks = previewByKind.get(kind) || [];
            if (blocks.length !== landmarks.length) continue;
            blocks.forEach((element, index) => element.dataset.sourceLine = String(landmarks[index].line));
        }
    },

    scanMarkdownLandmarks(markdown) {
        const lines = markdown.split('\n');
        const landmarks = [];
        let fence = null;
        let inTable = false;
        for (let index = 0; index < lines.length; index++) {
            const line = lines[index];
            const fenceMatch = line.match(/^ {0,3}(```+|~~~+)/);
            if (fence) {
                if (fenceMatch && fenceMatch[1][0] === fence.character && fenceMatch[1].length >= fence.length) {
                    fence = null;
                }
                continue;
            }
            if (fenceMatch) {
                fence = { character: fenceMatch[1][0], length: fenceMatch[1].length };
                landmarks.push({ line: index, kind: 'pre' });
                inTable = false;
                continue;
            }
            const heading = line.match(/^ {0,3}(#{1,6})\s+/);
            if (heading) {
                landmarks.push({ line: index, kind: `h${heading[1].length}` });
                inTable = false;
                continue;
            }
            if (index > 0 && /^ {0,3}(=+|-+)\s*$/.test(line) && lines[index - 1].trim()) {
                landmarks.push({ line: index - 1, kind: line.trim()[0] === '=' ? 'h1' : 'h2' });
                inTable = false;
                continue;
            }
            if (/^ {0,3}(?:(?:\*\s*){3,}|(?:-\s*){3,}|(?:_\s*){3,})$/.test(line)) {
                landmarks.push({ line: index, kind: 'hr' });
                inTable = false;
                continue;
            }
            if (/^\s*!\[[^\]]*\]\([^)]+\)\s*$/.test(line)) {
                landmarks.push({ line: index, kind: 'img' });
                inTable = false;
                continue;
            }
            const tableLine = /^\s*\|.*\|\s*$/.test(line);
            if (tableLine && !inTable) landmarks.push({ line: index, kind: 'table' });
            inTable = tableLine;
        }
        return landmarks;
    },

    previewBlockKind(element) {
        if (/^H[1-6]$/.test(element.tagName)) return element.tagName.toLowerCase();
        if (element.tagName === 'PRE') return 'pre';
        if (element.tagName === 'HR') return 'hr';
        if (element.tagName === 'TABLE') return 'table';
        if (element.tagName === 'IMG' ||
            (element.tagName === 'P' && element.childNodes.length === 1 && element.firstElementChild?.tagName === 'IMG')) return 'img';
        return null;
    },

    scheduleSave() {
        if (state.view !== 'editor') return;
        if (!isDraftDirty()) {
            this.setSaveStatus(state.noteID ? 'Salvo' : 'Rascunho', '');
            return;
        }
        window.clearTimeout(state.saveTimer);
        state.saveTimer = window.setTimeout(() => this.persistDraft(), 650);
        this.setSaveStatus('Alterações pendentes', 'pending');
    },

    async persistDraft() {
        if (state.savePromise) {
            await state.savePromise;
            return isDraftDirty() ? this.persistDraft() : true;
        }
        const draft = this.getDraft();
        if (!isDraftDirty(draft)) return true;
        const note = {
            title: draft.title.trim(),
            folder: draft.folder,
            content: draft.content
        };
        if (!note.title) {
            this.setSaveStatus('Informe um título para salvar', 'error');
            return false;
        }
        const id = state.noteID;
        if (id !== null) note.version = state.noteVersion;
        const endpoint = id === null ? '/api/notes' : `/api/notes/${id}`;
        const method = id === null ? 'POST' : 'PUT';
        this.setSaveStatus('Salvando…', 'pending');
        const job = async () => {
            try {
                let saved;
                try {
                    saved = await requestJSON(endpoint, { method, body: JSON.stringify(note) });
                } catch (error) {
                    if (error.status !== 409 || !error.body?.note || id === null) throw error;
                    const reload = window.confirm(
                        'Esta nota foi alterada em outra aba.\n\n' +
                        'OK: recarregar a versão mais recente e descartar suas mudanças.\n' +
                        'Cancelar: manter suas mudanças e sobrescrever a versão mais recente.'
                    );
                    if (reload) {
                        this.showEditor(error.body.note);
                        this.setSaveStatus('Versão mais recente carregada', 'saved');
                        return false;
                    }
                    state.noteVersion = error.body.note.version;
                    note.version = state.noteVersion;
                    saved = await requestJSON(endpoint, { method, body: JSON.stringify(note) });
                }
                state.noteVersion = saved.version ?? state.noteVersion;
                state.graphData = null;
                if (id === null) {
                    state.noteID = saved.id;
                    this.updatePreview();
                    document.querySelector('#deleteCurrentNote')?.hidden = false;
                    void this.loadAttachments(saved.id);
                    const current = history.state || {};
                    history.replaceState({ ...current, view: 'editor', noteId: saved.id, list: state.listState }, '', `?note=${saved.id}`);
                }
                void this.loadBacklinks(state.noteID);
                if (JSON.stringify(this.getDraft()) === JSON.stringify(draft)) {
                    document.querySelector('#noteTitle')?.value = saved.title;
                    state.noteFolder = saved.folder;
                    this.savedDraft = this.getDraft();
                    this.updateEditorBreadcrumb();
                } else {
                    this.savedDraft = draft;
                }
                this.updateEditorBreadcrumb();
                this.setSaveStatus('Salvo automaticamente', 'saved');
                state.folderNotesGeneration++;
                state.folderNotes.clear();
                void Promise.all([loadFolders(), loadNotes(), loadTags()]);
                void this.loadFolderNotes('', false);
                return true;
            } catch (error) {
                this.setSaveStatus(`Falha ao salvar: ${error.message}`, 'error');
                return false;
            }
        };
        state.savePromise = job;
        const result = await job;
        state.savePromise = null;
        if (result && isDraftDirty()) {
            window.clearTimeout(state.saveTimer);
            state.saveTimer = window.setTimeout(() => this.persistDraft(), 0);
        }
        return result;
    },

    async flushSave() {
        window.clearTimeout(state.saveTimer);
        while (state.savePromise) {
            await state.savePromise;
            if (state.savePromise) continue;
        }
        if (!isDraftDirty()) return true;
        if (!document.querySelector('#noteTitle').value.trim()) {
            this.setSaveStatus('Informe um título antes de sair', 'error');
            document.querySelector('#noteTitle')?.focus();
            return false;
        }
        const success = await this.persistDraft();
        if (!success) return false;
        if (isDraftDirty()) return this.flushSave();
        return true;
    },

    setSaveStatus(message, stateName) {
        const el = document.querySelector('#saveStatus');
        if (!el) return;
        el.textContent = message;
        el.dataset.state = stateName;
        el.className = `save-status${stateName ? ` is-${stateName}` : ''}`;
    },

    getDraft() {
        return {
            title: document.querySelector('#noteTitle')?.value || '',
            folder: state.noteFolder,
            content: this.textarea.value
        };
    },

    isDraftDirty(draft = this.getDraft()) {
        return JSON.stringify(draft) !== JSON.stringify(state.savedDraft);
    },

    showEditor(note) {
        window.clearTimeout(state.saveTimer);
        this.closeWikilinkAutocomplete();
        document.querySelector('#attachmentsDialog')?.close();
        if (window.matchMedia('(max-width: 700px)').matches) this.closePanel();
        document.body.classList.add('is-editor-view');
        state.view = 'editor';
        state.noteID = note?.id ?? null;
        state.noteVersion = note?.version ?? null;
        document.querySelector('#searchView')?.hidden = true;
        document.querySelector('#graphView')?.hidden = true;
        document.querySelector('#editorView')?.hidden = false;
        document.querySelector('#newNote')?.hidden = true;
        document.querySelector('#noteTitle')?.value = note?.title || '';
        state.frontmatterExpanded = false;
        state.noteFolder = note ? note.folder : state.folder;
        document.querySelector('#deleteCurrentNote')?.hidden = !note;
        document.querySelector('#attachmentList')?.replaceChildren();
        document.querySelector('#attachmentList')?.hidden = true;
        document.querySelector('#attachmentsEmpty')?.hidden = false;
        document.querySelector('#attachmentsButton')?.title = 'Abrir anexos';
        document.querySelector('#attachmentsButton')?.setAttribute('aria-label', 'Abrir anexos');
        this.resetBacklinks();
        document.querySelector('#saveStatus').className = 'save-status';
        document.querySelector('#saveStatus').textContent = note ? 'Salvo' : 'Rascunho';
        const content = note ? ensureFrontmatterTags(note.content, note.tags || []) : '';
        this.outline = extractMarkdownOutline(content);
        this.updateOutlineCount();
        renderedPreviewMarkdown = '';
        editorLinePositionCache = null;
        this.markdownEditor.setValue(content);
        const savedMode = localStorage.getItem('editorMode');
        const preferredMode = ['edit', 'split', 'preview'].includes(savedMode) ? savedMode : 'split';
        this.setEditorMode(window.matchMedia('(max-width: 700px)').matches && preferredMode === 'split'
            ? 'edit'
            : preferredMode);
        this.updatePreview();
        document.querySelector('#noteContent')?.focus();
        this.updateEditorBreadcrumb();
        if (note) {
            void this.loadAttachments(note.id);
            void this.loadBacklinks(note.id);
        } else if (document.querySelector('#panel').dataset.panel === 'backlinks') {
            this.renderBacklinksPanel();
        } else if (document.querySelector('#panel').dataset.panel === 'outline') {
            this.renderOutlinePanel();
        }
    },

    updateEditorBreadcrumb() {
        const el = document.querySelector('#currentFolder');
        if (!el) return;
        el.textContent = state.noteFolder || 'Sem pasta';
        const titleEl = document.querySelector('#noteTitle');
        if (titleEl) titleEl.title = titleEl.value || 'Título da nota';
    },

    refreshOpenNoteAfterMove() {
        if (state.view !== 'editor' || state.noteID === null || this.isDraftDirty()) return;
        const id = state.noteID;
        try {
            const note = await requestJSON(`/api/notes/${id}`);
            if (state.view !== 'editor' || state.noteID !== id || this.isDraftDirty()) return;
            state.noteFolder = note.folder;
            state.noteVersion = note.version ?? state.noteVersion;
            const content = ensureFrontmatterTags(note.content, note.tags || []);
            if (content !== document.querySelector('#noteContent')?.value) {
                document.querySelector('#noteContent')?.value = content;
                this.savedDraft = this.getDraft();
                this.markdownEditor.setValue(content);
                renderedPreviewMarkdown = '';
                editorLinePositionCache = null;
                this.outline = extractMarkdownOutline(content);
                this.updateOutlineCount();
                this.updatePreview();
            }
            this.savedDraft = this.getDraft();
            this.updateEditorBreadcrumb();
            void this.loadBacklinks(id);
        } catch (error) {
            // The move itself succeeded; keep the local editor state on failure.
        }
    },

    resetBacklinks() {
        state.backlinks = null;
        state.backlinksError = '';
        this.updateBacklinksCount();
        if (document.querySelector('#panel').dataset.panel === 'backlinks') this.renderBacklinksPanel();
    },

    updateBacklinksCount() {
        const count = state.view === 'editor' && state.noteID !== null ? state.backlinks?.length || 0 : 0;
        const el = document.querySelector('#backlinksCount');
        if (!el) return;
        el.textContent = count > 9 ? '9+' : String(count);
        el.hidden = count === 0;
        const label = count ? `Backlinks (${count})` : 'Abrir backlinks';
        document.querySelector('#backlinksButton')?.title = label;
        document.querySelector('#backlinksButton')?.setAttribute('aria-label', label);
    },

    renderBacklinksPanel(errorMessage = '') {
        const panelContent = document.querySelector('#panelContent');
        if (!panelContent) return;
        panelContent.replaceChildren();
        if (state.view !== 'editor' || state.noteID === null) {
            const empty = document.createElement('div');
            empty.className = 'backlinks-panel-empty';
            const message = document.createElement('p');
            message.textContent = 'Abra uma nota para ver os backlinks.';
            empty.append(message);
            panelContent.append(empty);
            return;
        }
        if (state.backlinksError || errorMessage) {
            const error = document.createElement('div');
            error.className = 'backlinks-panel-empty';
            const message = document.createElement('p');
            message.textContent = errorMessage || state.backlinksError;
            error.append(message);
            panelContent.append(error);
            return;
        }
        if (state.backlinks === null) {
            const loading = document.createElement('div');
            loading.className = 'backlinks-panel-empty';
            loading.textContent = 'Carregando backlinks…';
            panelContent.append(loading);
            return;
        }
        if (state.backlinks.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'backlinks-panel-empty';
            const message = document.createElement('p');
            message.textContent = 'Nenhuma nota aponta para esta nota.';
            empty.append(message);
            const hint = document.createElement('small');
            hint.textContent = 'Links [[wikilink]] e links Markdown são encontrados automaticamente.';
            empty.append(hint);
            panelContent.append(empty);
            return;
        }
        for (const note of state.backlinks) {
            const item = document.createElement('button');
            item.type = 'button';
            item.className = 'backlink-item';
            item.dataset.backlinkNote = String(note.id);
            item.title = note.folder ? `${note.folder}/${note.title}` : note.title;
            const title = document.createElement('span');
            title.className = 'backlink-item-title';
            title.textContent = note.title;
            item.append(title);
            if (note.folder) {
                const folder = document.createElement('span');
                folder.className = 'backlink-item-folder';
                folder.textContent = note.folder;
                item.append(folder);
            }
            if (note.preview) {
                const preview = document.createElement('span');
                preview.className = 'backlink-item-preview';
                preview.textContent = note.preview;
                item.append(preview);
            }
            panelContent.append(item);
        }
    },

    refreshOutline() {
        this.outline = extractMarkdownOutline(document.querySelector('#noteContent')?.value || '');
        this.updateOutlineCount();
        if (document.querySelector('#panel').dataset.panel === 'outline') this.renderOutlinePanel();
    },

    updateOutlineCount() {
        const count = state.view === 'editor' ? this.outline.length || 0 : 0;
        const el = document.querySelector('#outlineCount');
        if (!el) return;
        el.textContent = count > 9 ? '9+' : String(count);
        el.hidden = count === 0;
        const label = count ? `Sumário (${count})` : 'Abrir sumário da nota';
        document.querySelector('#outlineButton')?.title = label;
        document.querySelector('#outlineButton')?.setAttribute('aria-label', label);
    },

    renderOutlinePanel() {
        const panelContent = document.querySelector('#panelContent');
        if (!panelContent) return;
        panelContent.replaceChildren();
        if (state.view !== 'editor') {
            const empty = document.createElement('div');
            empty.className = 'outline-panel-empty';
            const message = document.createElement('p');
            message.textContent = 'Abra uma nota para ver o sumário.';
            empty.append(message);
            panelContent.append(empty);
            return;
        }
        if (this.outline.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'outline-panel-empty';
            const message = document.createElement('p');
            message.textContent = 'Esta nota não tem títulos.';
            empty.append(message);
            panelContent.append(empty);
            return;
        }
        for (const heading of this.outline) {
            const item = document.createElement('button');
            item.type = 'button';
            item.className = 'outline-item';
            item.dataset.outlineLine = String(heading.line);
            item.dataset.level = String(heading.level);
            item.style.paddingLeft = `${10 + (heading.level - 1) * 12}px`;
            item.textContent = heading.text;
            item.title = heading.text;
            panelContent.append(item);
        }
    },

    scrollToOutlineHeading(line) {
        const lineIndex = Number(line);
        const heading = this.outline.find(item => item.line === lineIndex);
        if (!heading) return;
        const mode = document.querySelector('.editor-workspace')?.dataset.editorMode;
        if (mode !== 'edit' && renderedPreviewMarkdown !== stripFrontmatter(document.querySelector('#noteContent')?.value)) {
            this.pendingOutlineLine = lineIndex;
            this.updatePreview();
            return;
        }
        if (mode !== 'preview') {
            const markdown = document.querySelector('#noteContent')?.value;
            if (!markdown) return;
            const lines = markdown.split('\n');
            const lineTop = this.measureEditorLines(markdown, [heading.line]).get(heading.line);
            let caret = 0;
            for (let index = 0; index < heading.line; index++) caret += lines[index].length + 1;
            const textarea = document.querySelector('#noteContent');
            if (!textarea) return;
            textarea.focus();
            textarea.setSelectionRange(caret, caret);
            requestAnimationFrame(() => {
                if (!Number.isFinite(lineTop)) return;
                textarea.scrollTop = Math.max(0, lineTop - textarea.clientHeight / 3);
                this.handleEditorScroll(textarea);
            });
        }

        if (mode !== 'edit') {
            const headings = [...document.querySelectorAll('#markdownPreview h1, h2, h3, h4, h5, h6')];
            const headingIndex = this.outline.indexOf(heading);
            const previewHeading = headings.find(element =>
                Number(element.dataset.sourceLine) === heading.bodyLine
            ) || headings[headingIndex] || headings.find(element =>
                element.textContent.trim() === heading.text
            );
            if (!previewHeading) return;
            requestAnimationFrame(() => {
                const preview = document.querySelector('#markdownPreview');
                if (!preview) return;
                const targetTop = Math.max(0, Math.min(
                    preview.scrollHeight - preview.clientHeight,
                    preview.scrollTop + previewHeading.getBoundingClientRect().top - preview.getBoundingClientRect().top
                ));
                if (Math.abs(preview.scrollTop - targetTop) >= 1) {
                    this.programmaticScroll = { element: preview, scrollTop: targetTop };
                    preview.scrollTop = targetTop;
                }
            });
        }
    },

    measureEditorLines(markdown, lines) {
        const editor = document.querySelector('#noteContent');
        if (!editor) return new Map();
        const computed = window.getComputedStyle(editor);
        const cacheKey = [
            markdown, editor.clientWidth, computed.fontFamily, computed.fontSize,
            computed.fontWeight, computed.lineHeight, computed.letterSpacing,
            computed.tabSize, computed.padding, [...lines].sort((a, b) => a - b).join(',')
        ].join('|');
        if (this.editorLinePositionCache?.key === cacheKey) return this.editorLinePositionCache.positions;

        const mirror = document.createElement('div');
        for (const property of [
            'direction', 'fontFamily', 'fontStyle', 'fontVariant', 'fontWeight',
            'fontStretch', 'fontSize', 'fontSizeAdjust', 'lineHeight',
            'letterSpacing', 'wordSpacing', 'tabSize', 'textIndent', 'textAlign',
            'textTransform', 'textDecoration'
        ]) mirror.style[property] = computed[property];
        const paddingLeft = parseFloat(computed.paddingLeft) || 0;
        const paddingRight = parseFloat(computed.paddingRight) || 0;
        mirror.style.boxSizing = 'content-box';
        mirror.style.width = `${Math.max(0, editor.clientWidth - paddingLeft - paddingRight)}px`;
        mirror.style.padding = computed.padding;
        mirror.style.border = '0';
        mirror.style.height = 'auto';
        mirror.style.overflow = 'hidden';
        mirror.style.position = 'absolute';
        mirror.style.left = '-100000px';
        mirror.style.top = '0';
        mirror.style.visibility = 'hidden';
        mirror.style.whiteSpace = 'pre-wrap';
        mirror.style.wordWrap = 'break-word';
        mirror.style.overflowWrap = 'break-word';
        mirror.style.height = 'auto';
        const wanted = new Set(lines);
        const sourceLines = markdown.split('\n');
        sourceLines.forEach((line, index) => {
            if (wanted.has(index)) {
                const marker = document.createElement('span');
                marker.dataset.line = String(index);
                marker.textContent = line || '\u200b';
                mirror.append(marker);
            } else {
                mirror.append(document.createTextNode(line));
            }
            if (index < sourceLines.length - 1) mirror.append(document.createTextNode('\n'));
        });
        document.body.append(mirror);
        const mirrorTop = mirror.getBoundingClientRect().top;
        const positions = new Map([...mirror.querySelectorAll('[data-line]')].map(marker => [
            Number(marker.dataset.line),
            marker.getBoundingClientRect().top - mirrorTop
        ]));
        mirror.remove();
        this.editorLinePositionCache = { key: cacheKey, positions };
        return positions;
    },

    getScrollLandmarks() {
        const content = document.querySelector('#noteContent')?.value;
        if (!content) return [];
        const markdown = stripFrontmatter(content);
        if (renderedPreviewMarkdown !== markdown) return [];
        const editor = document.querySelector('#noteContent');
        const preview = document.querySelector('#markdownPreview');
        if (!editor || !preview) return [];
        const editorScrollable = Math.max(0, editor.scrollHeight - editor.clientHeight);
        const previewScrollable = Math.max(0, preview.scrollHeight - preview.clientHeight);
        if (!editorScrollable || !previewScrollable) return [];
        const sourceLineOffset = this.frontmatterLineOffset(content);
        const sourceLines = [...document.querySelectorAll('#markdownPreview [data-source-line]')]
            .map(element => Number(element.dataset.sourceLine));
        const positions = this.measureEditorLines(content, sourceLines.map(line => line + sourceLineOffset));
        const previewRect = preview.getBoundingClientRect();
        return [...document.querySelectorAll('#markdownPreview [data-source-line]')]
            .map(element => {
                const sourceY = positions.get(Number(element.dataset.sourceLine) + sourceLineOffset);
                const previewTop = element.getBoundingClientRect().top - previewRect.top + preview.scrollTop;
                return {
                    editorPct: Number.isFinite(sourceY) ? sourceY / editorScrollable : NaN,
                    previewTop
                };
            })
            .filter(anchor => anchor.editorPct > 0 && anchor.editorPct < 1 &&
                anchor.previewTop > 0 && anchor.previewTop < previewScrollable);
    },

    handleEditorScroll(source) {
        if (this.programmaticScroll?.element === source) {
            const matched = Math.abs(source.scrollTop - this.programmaticScroll.scrollTop) < 1;
            this.programmaticScroll = null;
            if (matched) return;
        }
        const pendingScrollSource = source;
        if (this.scrollSyncFrame) return;
        this.scrollSyncFrame = requestAnimationFrame(() => {
            this.scrollSyncFrame = 0;
            const source = pendingScrollSource;
            pendingScrollSource = null;
            if (!source) return;
            const landmarks = this.getScrollLandmarks();
            const fromEditor = source === document.querySelector('#noteContent');
            const sourceScrollable = Math.max(0, source.scrollHeight - source.clientHeight);
            const target = fromEditor ? document.querySelector('#markdownPreview') : document.querySelector('#noteContent');
            const targetScrollable = Math.max(0, target.scrollHeight - target.clientHeight);
            const targetTop = fromEditor
                ? this.mapEditorToPreview(source.scrollTop, sourceScrollable, targetScrollable, landmarks)
                : this.mapPreviewToEditor(source.scrollTop, sourceScrollable, targetScrollable, landmarks);
            if (Math.abs(target.scrollTop - targetTop) >= 1) {
                this.programmaticScroll = { element: target, scrollTop: targetTop };
                target.scrollTop = targetTop;
            }
        });
    },

    syncEditorScrollFromSource() {
        const editor = document.querySelector('#noteContent');
        const preview = document.querySelector('#markdownPreview');
        if (!editor || !preview) return;
        const targetTop = this.mapEditorToPreview(
            editor.scrollTop,
            Math.max(0, editor.scrollHeight - editor.clientHeight),
            Math.max(0, preview.scrollHeight - preview.clientHeight),
            this.getScrollLandmarks()
        );
        if (Math.abs(preview.scrollTop - targetTop) >= 1) {
            this.programmaticScroll = { element: preview, scrollTop: targetTop };
            preview.scrollTop = targetTop;
        }
    },

    mapEditorToPreview(editorScrollTop, editorScrollable, previewScrollable, landmarks) {
        // Simplified mapping
        if (editorScrollable === 0 || previewScrollable === 0) return 0;
        return (editorScrollTop / editorScrollable) * previewScrollable;
    },

    mapPreviewToEditor(previewScrollTop, editorScrollable, previewScrollable, landmarks) {
        // Simplified mapping
        if (editorScrollable === 0 || previewScrollable === 0) return 0;
        return (previewScrollTop / previewScrollable) * editorScrollable;
    },

    updateEditorHistoryButtons({ canUndo, canRedo }) {
        const undoBtn = document.querySelector('[data-action="undo"]');
        const redoBtn = document.querySelector('[data-action="redo"]');
        if (undoBtn) undoBtn.disabled = !canUndo;
        if (redoBtn) redoBtn.disabled = !canRedo;
    },

    setEditorMode(mode) {
        const workspace = document.querySelector('.editor-workspace');
        if (!workspace) return;
        // Simplified mode switching - full logic would need more context
        workspace.dataset.editorMode = mode;
        localStorage.setItem('editorMode', mode);
        document.querySelectorAll('[data-editor-mode]').forEach(button => {
            const active = button.dataset.editorMode === mode;
            button.classList.toggle('is-active', active);
            button.setAttribute('aria-pressed', String(active));
        });
        if (mode === 'edit') {
            window.clearTimeout(previewTimer);
            previewRequestID++;
        } else {
            this.updatePreview();
        }
        requestAnimationFrame(() => {
            this.syncPreviewMinimumHeight();
            this.syncEditorScrollFromSource();
        });
    },

    togglePanel(name) {
        const panel = document.querySelector('#workspacePanel');
        const railButtons = document.querySelectorAll('.rail-button[data-panel]');
        if (!panel || !railButtons) return;
        if (panel.dataset.panel === name && document.body.classList.contains('panel-open')) {
            this.closePanel();
            return;
        }
        panel.dataset.panel = name;
        document.body.classList.add('panel-open');
        railButtons.forEach(button => button.classList.toggle('is-active', button.dataset.panel === name));
        if (name === 'backlinks') {
            document.querySelector('#panelTitle')?.textContent = 'Backlinks';
            this.renderBacklinksPanel();
            if (state.noteID !== null && state.backlinks === null) void this.loadBacklinks(state.noteID);
        } else if (name === 'outline') {
            document.querySelector('#panelTitle')?.textContent = 'Sumário';
            this.renderOutlinePanel();
        } else if (name === 'settings') {
            document.querySelector('#panelTitle')?.textContent = 'Configurações';
            const placeholder = document.createElement('p');
            placeholder.className = 'settings-placeholder';
            placeholder.textContent = 'As configurações da aplicação aparecerão aqui.';
            document.querySelector('#panelContent')?.replaceChildren(placeholder);
        } else {
            document.querySelector('#panelTitle')?.textContent = 'Pastas e notas';
            this.renderFolderPanel();
        }
    },

    closePanel() {
        document.body.classList.remove('panel-open');
        document.querySelectorAll('.rail-button[data-panel]').forEach(button => button.classList.remove('is-active'));
    },

    loadAttachments(noteID) {
        // Would call API - simplified
    },

    loadBacklinks(noteID) {
        // Would call API - simplified
    },

    loadFolderNotes(folder, more = false) {
        // Would call API - simplified
    },

    closeWikilinkAutocomplete() {
        window.clearTimeout(state.wikilinkTimer);
        state.wikilinkRequestID++;
        state.wikilinkAutocomplete.open = false;
        state.wikilinkAutocomplete.items = [];
        const autocomplete = document.querySelector('#wikilinkAutocomplete');
        if (autocomplete) {
            autocomplete.hidden = true;
            document.querySelector('#noteContent')?.setAttribute('aria-expanded', 'false');
            document.querySelector('#noteContent')?.removeAttribute('aria-activedescendant');
        }
    },

    acceptWikilink(item) {
        this.closeWikilinkAutocomplete();
        this.markdownEditor.acceptWikilink(item.insert);
    }
};