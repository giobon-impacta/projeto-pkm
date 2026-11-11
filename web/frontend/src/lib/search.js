// Search module - note search, filtering, and pagination functionality
import { requestJSON } from './api-client.js';

export const searchModule = {
    // Initialize search functionality
    init() {
        this.attachSearchListener();
        this.attachModeButtons();
        this.attachPagination();
        this.attachTagFiltering();
    },

    // Attach search input debounced listener
    attachSearchListener() {
        const input = document.querySelector('#searchInput');
        if (!input) return;
        let timeout;
        input.addEventListener('input', () => {
            clearTimeout(timeout);
            timeout = setTimeout(() => {
                this.loadNotes();
                this.updateListHistory();
            }, 160);
        });
    },

    // Attach search mode buttons
    attachModeButtons() {
        const modeButtons = document.querySelectorAll('[data-mode]');
        if (!modeButtons.length) return;
        // Initialize active state
        const firstButton = modeButtons[0];
        if (firstButton) firstButton.classList.add('is-active');
        firstButton.setAttribute('aria-pressed', 'true');

        modeButtons.forEach(button => {
            button.addEventListener('click', () => {
                // Update active state
                modeButtons.forEach(b => {
                    b.classList.toggle('is-active', b === button);
                    b.setAttribute('aria-pressed', String(b === button));
                });
                state.mode = button.dataset.mode;
                this.loadNotes();
                this.updateListHistory();
            });
        });
    },

    // Attach pagination buttons
    attachPagination() {
        const prev = document.querySelector('#previousPage');
        const next = document.querySelector('#nextPage');
        if (!prev || !next) return;

        prev.addEventListener('click', () => {
            state.offset = Math.max(0, state.offset - pageSize);
            this.loadNotes();
            this.updateListHistory();
        });

        next.addEventListener('click', () => {
            if (state.offset + pageSize < state.total) {
                state.offset += pageSize;
                this.loadNotes();
                this.updateListHistory();
            }
        });
    },

    // Attach tag filtering
    attachTagFiltering() {
        const tagFilterBar = document.querySelector('#tagFilterBar');
        if (!tagFilterBar) return;

        tagFilterBar.addEventListener('click', event => {
            const button = event.target.closest('[data-tag]');
            if (!button) return;
            const tag = button.dataset.tag || '';
            this.selectTag(tag.toLowerCase() === state.tag.toLowerCase() ? '' : tag);
        });
    },

    // Load notes from API
    async loadNotes() {
        const requestID = ++state.requestID;
        const query = document.querySelector('#searchInput').value.trim();
        const params = new URLSearchParams({
            folder: state.folder,
            limit: String(pageSize),
            offset: String(state.offset)
        });
        if (state.tag) params.set('tag', state.tag);
        const endpoint = query ? '/api/search' : '/api/notes';
        if (query) {
            params.set('q', query);
            params.set('mode', state.mode);
        }
        try {
            const page = await requestJSON(`${endpoint}?${params}`);
            if (requestID !== state.requestID) return;
            if (page.total > 0 && state.offset >= page.total) {
                state.offset = Math.floor((page.total - 1) / pageSize) * pageSize;
                return this.loadNotes();
            }
            state.notes = page.items;
            state.total = page.total;
            this.renderNotes(page);
        } catch (error) {
            if (requestID === state.requestID) this.showMessage(`Não foi possível carregar as notas: ${error.message}`, 'error');
        }
    },

    // Render notes list
    renderNotes(page) {
        const notesList = document.querySelector('#notesList');
        if (!notesList) return;
        notesList.replaceChildren();
        const query = document.querySelector('#searchInput').value.trim();
        const tagLabel = state.tag ? `#${state.tag}` : '';
        if (query) {
            document.querySelector('#resultsTitle').textContent = tagLabel
                ? `Resultados para “${query}” em ${tagLabel}`
                : `Resultados para “${query}”`;
        } else if (tagLabel) {
            document.querySelector('#resultsTitle').textContent = `Notas com ${tagLabel}`;
        } else {
            document.querySelector('#resultsTitle').textContent = state.folder ? state.folder.split('/').at(-1) : 'Todas as notas';
        }
        document.querySelector('#resultCount').textContent = `${page.total} ${page.total === 1 ? 'nota' : 'notas'}`;
        if (state.view === 'list') {
            document.querySelector('#currentFolder').textContent = state.folder || 'Todas as notas';
        }
        document.querySelector('#clearFolder').hidden = !state.folder;
        document.querySelector('#clearTag').hidden = !state.tag;
        if (page.items.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'empty-state';
            if (query) empty.textContent = 'Nenhuma nota corresponde à pesquisa.';
            else if (state.tag) empty.textContent = `Nenhuma nota com a tag #${state.tag}.`;
            else empty.textContent = 'Ainda não há notas. Crie uma para começar.';
            notesList.append(empty);
        } else {
            const contentSearch = Boolean(query && state.mode === 'content');
            for (const note of page.items) notesList.append(this.createNoteCard(note, contentSearch));
        }
        this.renderTagFilter();
        const first = page.total === 0 ? 0 : page.offset + 1;
        const last = Math.min(page.offset + page.items.length, page.total);
        document.querySelector('#pagination')?.hidden = page.total <= page.limit;
        document.querySelector('#pageStatus')?.textContent = `${first}–${last} de ${page.total}`;
        document.querySelector('#previousPage')?.disabled = page.offset === 0;
        document.querySelector('#nextPage')?.disabled = page.offset + page.limit >= page.total;
    },

    createNoteCard(note, contentSearch) {
        const card = document.createElement('article');
        card.className = `note-card${contentSearch ? ' is-content-result' : ''}`;
        card.dataset.noteId = String(note.id);
        const main = document.createElement('button');
        main.className = 'note-card-main';
        main.type = 'button';
        main.dataset.action = 'open';
        const title = document.createElement('span');
        title.className = 'note-title';
        title.textContent = note.title;
        main.append(title);
        if (contentSearch) {
            const preview = document.createElement('span');
            preview.className = 'note-preview';
            this.appendHighlightedPreview(preview, note.preview || 'Nenhum trecho disponível.');
            main.append(preview);
        }
        const info = document.createElement('div');
        info.className = 'note-meta';
        if (note.folder) {
            const folder = document.createElement('span');
            folder.className = 'note-folder';
            folder.textContent = note.folder;
            info.append(folder);
        }
        const tags = document.createElement('div');
        tags.className = 'note-tags';
        for (const value of note.tags || []) {
            const tag = document.createElement('button');
            tag.type = 'button';
            tag.className = `note-tag${value.toLowerCase() === state.tag.toLowerCase() ? ' is-active' : ''}`;
            tag.dataset.action = 'tag';
            tag.dataset.tag = value;
            tag.title = `Filtrar por #${value}`;
            tag.textContent = `#${value}`;
            tags.append(tag);
        }
        info.append(tags);
        const actions = document.createElement('div');
        actions.className = 'note-actions';
        const remove = document.createElement('button');
        remove.className = 'icon-button danger-hover';
        remove.type = 'button';
        remove.dataset.action = 'delete';
        remove.setAttribute('aria-label', `Excluir ${note.title}`);
        remove.title = 'Excluir';
        remove.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M10 11v6m4-6v6M6 7l1 14h10l1-14M9 7V4h6v3"/></svg>';
        actions.append(remove);
        card.append(main, info, actions);
        return card;
    },

    appendHighlightedPreview(container, text) {
        const startMarker = '<mark>';
        const endMarker = '</mark>';
        let remaining = text;
        while (remaining) {
            const start = remaining.indexOf(startMarker);
            if (start < 0) {
                container.append(document.createTextNode(remaining));
                break;
            }
            if (start > 0) container.append(document.createTextNode(remaining.slice(0, start)));
            const contentStart = start + startMarker.length;
            const end = remaining.indexOf(endMarker, contentStart);
            if (end < 0) {
                container.append(document.createTextNode(remaining.slice(start)));
                break;
            }
            const highlight = document.createElement('mark');
            highlight.textContent = remaining.slice(contentStart, end);
            container.append(highlight);
            remaining = remaining.slice(end + endMarker.length);
        }
    },

    renderTagFilter() {
        const element = document.querySelector('#tagFilterBar');
        if (!element) return;
        const tags = state.tags.filter(tag => tag && tag.name);
        element.hidden = tags.length === 0;
        element.replaceChildren();
        for (const tag of tags) {
            const button = document.createElement('button');
            button.type = 'button';
            button.className = `tag-filter${tag.name.toLowerCase() === state.tag.toLowerCase() ? ' is-active' : ''}`;
            button.dataset.tag = tag.name;
            button.setAttribute('aria-pressed', String(tag.name.toLowerCase() === state.tag.toLowerCase()));
            button.title = `Filtrar por #${tag.name}`;
            const name = document.createElement('span');
            name.textContent = `#${tag.name}`;
            const count = document.createElement('span');
            count.className = 'tag-filter-count';
            count.textContent = String(tag.count);
            button.append(name, count);
            element.append(button);
        }
    },

    async loadFolders() {
        try {
            const folders = await requestJSON('/api/folders');
            if (!Array.isArray(folders)) throw new Error('resposta de pastas inválida');
            return folders;
        } catch (error) {
            this.showMessage(`Não foi possível carregar as pastas: ${error.message}`, 'error');
        }
    },

    async loadTags() {
        try {
            const tags = await requestJSON('/api/tags');
            if (!Array.isArray(tags)) throw new Error('resposta de tags inválida');
            state.tags = tags;
            this.renderTagFilter();
        } catch (error) {
            state.tags = [];
            this.renderTagFilter();
        }
    },

    selectTag(tag) {
        state.tag = tag;
        state.offset = 0;
        this.loadNotes();
        this.updateListHistory();
        const searchInput = document.querySelector('#searchInput');
        if (searchInput && document.querySelector('#panel').dataset.panel === 'search') searchInput.focus();
    },

    updateListHistory() {
        if (state.view === 'list') {
            const searchInput = document.querySelector('#searchInput');
            const url = new URLSearchParams();
            url.set('view', 'list');
            url.set('list', JSON.stringify({
                query: searchInput ? searchInput.value : state.folder,
                mode: state.mode,
                folder: state.folder,
                tag: state.tag,
                offset: state.offset,
                graphVisible: state.graphVisible
            }));
            history.replaceState({ view: 'list', list: captureListState() }, '', location.href);
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