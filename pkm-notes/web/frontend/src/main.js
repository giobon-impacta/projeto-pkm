import { ensureFrontmatterTags, extractMarkdownOutline, MarkdownEditor, parseFrontmatter, stripFrontmatter } from './lib/markdown-editor.js';
import { GraphView } from './lib/graph-view.js';
import { getScrollSpacerHeight, mapEditorToPreview, mapPreviewToEditor } from './lib/scroll-sync.js';

const pageSize = 30;
const treePageSize = 100;
const state = {
    mode: 'title',
    folder: '',
    offset: 0,
    total: 0,
    notes: [],
    folders: [],
    folderNotes: new Map(),
    folderNotesLoading: new Set(),
    folderNotesGeneration: 0,
    expandedFolders: new Set(['']),
    view: 'list',
    graphVisible: false,
    graphData: null,
    graphRequestID: 0,
    backlinks: null,
    backlinksError: '',
    backlinksRequestID: 0,
    outline: [],
    pendingOutlineLine: null,
    noteID: null,
    noteFolder: '',
    frontmatterExpanded: false,
    savedDraft: null,
    listState: null,
    directEntry: false,
    requestID: 0,
    navigationID: 0,
    searchTimer: null,
    saveTimer: null,
    savePromise: null,
    wikilinkRequestID: 0,
    wikilinkTimer: null,
    wikilinkAutocomplete: {
        open: false,
        items: [],
        activeIndex: 0
    }
};
let editorSplitPercent = 50;
let workspacePanelWidth = 280;

const elements = {
    searchView: document.querySelector('#searchView'),
    graphView: document.querySelector('#graphView'),
    editorView: document.querySelector('#editorView'),
    searchInput: document.querySelector('#searchInput'),
    notesList: document.querySelector('#notesList'),
    resultCount: document.querySelector('#resultCount'),
    resultsTitle: document.querySelector('#resultsTitle'),
    pagination: document.querySelector('#pagination'),
    pageStatus: document.querySelector('#pageStatus'),
    previousPage: document.querySelector('#previousPage'),
    nextPage: document.querySelector('#nextPage'),
    panel: document.querySelector('#workspacePanel'),
    panelResizer: document.querySelector('#workspacePanelResizer'),
    panelTitle: document.querySelector('#panelTitle'),
    panelContent: document.querySelector('#panelContent'),
    backlinksCount: document.querySelector('#backlinksCount'),
    outlineCount: document.querySelector('#outlineCount'),
    currentFolder: document.querySelector('#currentFolder'),
    noteTitle: document.querySelector('#noteTitle'),
    noteContent: document.querySelector('#noteContent'),
    markdownPreview: document.querySelector('#markdownPreview'),
    wikilinkAutocomplete: document.querySelector('#wikilinkAutocomplete'),
    attachmentsDialog: document.querySelector('#attachmentsDialog'),
    attachmentsButton: document.querySelector('#attachmentsButton'),
    attachmentInput: document.querySelector('#attachmentInput'),
    attachmentList: document.querySelector('#attachmentList'),
    attachmentsEmpty: document.querySelector('#attachmentsEmpty'),
    editorWorkspace: document.querySelector('.editor-workspace'),
    saveStatus: document.querySelector('#saveStatus'),
    appMessage: document.querySelector('#appMessage'),
    newNote: document.querySelector('#newNote'),
    deleteCurrentNote: document.querySelector('#deleteCurrentNote')
};

let previewTimer;
let previewRequestID = 0;
let renderedPreviewMarkdown = '';
let editorLinePositionCache = null;
let scrollSyncFrame = 0;
let pendingScrollSource = null;
let programmaticScroll = null;
const markdownEditor = new MarkdownEditor(elements.noteContent, () => {
    syncPreviewMinimumHeight();
    updatePreview();
    scheduleSave();
    refreshOutline();
}, updateEditorHistoryButtons);
const graphView = new GraphView(document.querySelector('#noteGraph'), id => void openNote(id));
document.querySelector('#graphButton').addEventListener('click', () => {
    void openGraphView().catch(error => {
        showMessage(`Não foi possível abrir o grafo: ${error.message}`, 'error');
    });
});

document.addEventListener('DOMContentLoaded', () => void initialize());

function startSplitResize(event) {
    if (event.button !== 0 || !window.matchMedia('(min-width: 701px)').matches) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    document.body.classList.add('is-resizing-editor');
    resizeSplit(event);
}

function resizeSplit(event) {
    if (!event.currentTarget.hasPointerCapture?.(event.pointerId)) return;
    const bounds = elements.editorWorkspace.getBoundingClientRect();
    if (!bounds.width) return;
    editorSplitPercent = Math.min(80, Math.max(20, ((event.clientX - bounds.left) / bounds.width) * 100));
    updateSplitRatio();
    syncPreviewMinimumHeight();
    syncEditorScrollFromSource();
}

function stopSplitResize(event) {
    if (!event.currentTarget.hasPointerCapture?.(event.pointerId)) return;
    event.currentTarget.releasePointerCapture(event.pointerId);
    document.body.classList.remove('is-resizing-editor');
    localStorage.setItem('editorSplitPercent', String(Math.round(editorSplitPercent)));
}

function handleSplitResizeKey(event) {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    if (event.key === 'Home') editorSplitPercent = 20;
    else if (event.key === 'End') editorSplitPercent = 80;
    else editorSplitPercent = Math.min(80, Math.max(20, editorSplitPercent + (event.key === 'ArrowRight' ? 5 : -5)));
    updateSplitRatio();
    localStorage.setItem('editorSplitPercent', String(Math.round(editorSplitPercent)));
}

function updateSplitRatio() {
    const splitter = document.querySelector('.editor-splitter');
    if (!splitter) return;
    elements.editorWorkspace.style.setProperty('--editor-left', `${editorSplitPercent}fr`);
    elements.editorWorkspace.style.setProperty('--editor-right', `${100 - editorSplitPercent}fr`);
    splitter.setAttribute('aria-valuenow', String(Math.round(editorSplitPercent)));
}

async function initialize() {
    setupEventListeners();
    try {
        const response = await fetch('/api/auth/status');
        if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
        const auth = await response.json();
        document.querySelector('#logoutButton').hidden = !auth.enabled;
        if (!auth.authenticated) {
            location.replace('/login.html');
            return;
        }
    } catch (error) {
        showMessage(`Não foi possível verificar o acesso: ${error.message}`, 'error');
        return;
    }
    setupTheme();
    const savedPanelWidth = Number(localStorage.getItem('workspacePanelWidth'));
    if (Number.isFinite(savedPanelWidth) && savedPanelWidth > 0) workspacePanelWidth = savedPanelWidth;
    setWorkspacePanelWidth(workspacePanelWidth);
    const savedSplit = Number(localStorage.getItem('editorSplitPercent'));
    if (Number.isFinite(savedSplit)) editorSplitPercent = Math.min(80, Math.max(20, savedSplit));
    updateSplitRatio();
    if (window.matchMedia('(max-width: 700px)').matches) closePanel();
    const initialListState = captureListState();
    const noteParam = new URLSearchParams(location.search).get('note');
    if (/^\d+$/.test(noteParam || '')) {
        state.directEntry = true;
        state.listState = initialListState;
        history.replaceState({ view: 'editor', noteId: Number(noteParam), list: initialListState, direct: true }, '', location.href);
        void openNote(Number(noteParam), { push: false });
    } else {
        history.replaceState({ view: 'list', list: initialListState }, '', location.href);
        loadNotes();
    }

    loadFolders();
    void loadFolderNotes('', false);
}

function setupEventListeners() {
    document.querySelectorAll('.rail-button[data-panel]').forEach(button => {
        button.addEventListener('click', () => togglePanel(button.dataset.panel));
    });
    document.addEventListener('click', event => {
        if (!document.body.classList.contains('panel-open')) return;
        if (elements.panel.contains(event.target) ||
            elements.panelResizer.contains(event.target) ||
            event.target.closest('.rail-button[data-panel]')) return;
        closePanel();
    });
    document.querySelector('#homeButton').addEventListener('click', () => {
        if (state.view === 'editor') navigateBack();
        else if (state.graphVisible) closeGraphView();
        else elements.searchInput.focus();
    });
    document.querySelector('#closePanel').addEventListener('click', closePanel);
    elements.panelResizer.addEventListener('pointerdown', startWorkspacePanelResize);
    elements.panelResizer.addEventListener('pointermove', resizeWorkspacePanel);
    elements.panelResizer.addEventListener('pointerup', stopWorkspacePanelResize);
    elements.panelResizer.addEventListener('pointercancel', stopWorkspacePanelResize);
    elements.panelResizer.addEventListener('keydown', handleWorkspacePanelResizeKey);
    window.addEventListener('resize', () => setWorkspacePanelWidth(workspacePanelWidth));
    document.querySelector('#closeGraph').addEventListener('click', closeGraphView);
    document.querySelector('#fitGraph').addEventListener('click', () => graphView.fit());
    document.querySelector('#graphSearch').addEventListener('input', event => graphView.setSearch(event.target.value));
    elements.newNote.addEventListener('click', () => openNewNote());
    elements.deleteCurrentNote.addEventListener('click', deleteCurrentNote);
    document.querySelector('#logoutButton').addEventListener('click', async () => {
        try {
            const response = await fetch('/api/auth/logout', { method: 'POST' });
            if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
            location.replace('/login.html');
        } catch (error) {
            showMessage(`Não foi possível encerrar a sessão: ${error.message}`, 'error');
        }
    });
    elements.attachmentsButton.addEventListener('click', () => {
        elements.attachmentsDialog.showModal();
        if (state.noteID !== null) void loadAttachments(state.noteID);
    });
    document.querySelector('#closeAttachments').addEventListener('click', () => elements.attachmentsDialog.close());
    elements.attachmentsDialog.addEventListener('click', event => {
        if (event.target === elements.attachmentsDialog) elements.attachmentsDialog.close();
    });
    document.querySelector('#addAttachment').addEventListener('click', () => elements.attachmentInput.click());
    elements.attachmentInput.addEventListener('change', event => {
        const files = [...event.target.files];
        event.target.value = '';
        if (files.length) void uploadAttachments(files);
    });
    elements.attachmentList.addEventListener('click', event => {
        const button = event.target.closest('[data-delete-attachment]');
        if (button) void deleteAttachment(Number(button.dataset.deleteAttachment));
    });
    document.querySelector('#themeToggle').addEventListener('click', toggleTheme);
    document.querySelector('#clearFolder').addEventListener('click', () => selectFolder(''));
    elements.searchInput.addEventListener('input', () => {
        window.clearTimeout(state.searchTimer);
        state.offset = 0;
        state.searchTimer = window.setTimeout(() => {
            loadNotes();
            updateListHistory();
        }, 160);
    });
    document.querySelectorAll('[data-mode]').forEach(button => {
        button.addEventListener('click', () => {
            state.mode = button.dataset.mode;
            updateSearchModes();
            state.offset = 0;
            loadNotes();
            updateListHistory();
        });
    });
    elements.previousPage.addEventListener('click', () => {
        state.offset = Math.max(0, state.offset - pageSize);
        loadNotes();
        updateListHistory();
    });
    elements.nextPage.addEventListener('click', () => {
        if (state.offset + pageSize < state.total) {
            state.offset += pageSize;
            loadNotes();
            updateListHistory();
        }
    });
    elements.panelContent.addEventListener('click', handlePanelClick);
    elements.panelContent.addEventListener('dragstart', handleSidebarDragStart);
    elements.panelContent.addEventListener('dragend', clearDropTargets);
    elements.panelContent.addEventListener('dragover', handleSidebarDragOver);
    elements.panelContent.addEventListener('dragleave', handleSidebarDragLeave);
    elements.panelContent.addEventListener('drop', event => void handleSidebarDrop(event));
    elements.notesList.addEventListener('click', handleNoteListClick);
    elements.noteContent.addEventListener('input', () => {
        markdownEditor.handleInput();
        syncPreviewMinimumHeight();
        scheduleWikilinkSuggestions();
    });
    elements.noteContent.addEventListener('keydown', event => {
        if (!handleWikilinkKeydown(event)) markdownEditor.handleKeydown(event);
    });
    elements.noteContent.addEventListener('keyup', event => {
        if (!['ArrowUp', 'ArrowDown', 'Enter', 'Tab', 'Escape'].includes(event.key)) {
            scheduleWikilinkSuggestions();
        }
    });
    elements.noteContent.addEventListener('click', scheduleWikilinkSuggestions);
    elements.noteContent.addEventListener('blur', () => {
        window.setTimeout(closeWikilinkAutocomplete, 100);
    });
    elements.wikilinkAutocomplete.addEventListener('mousedown', event => event.preventDefault());
    elements.wikilinkAutocomplete.addEventListener('click', event => {
        const option = event.target.closest('[data-wikilink-index]');
        if (!option) return;
        const item = state.wikilinkAutocomplete.items[Number(option.dataset.wikilinkIndex)];
        if (item) acceptWikilinkSuggestion(item);
    });
    elements.wikilinkAutocomplete.addEventListener('mouseover', event => {
        const option = event.target.closest('[data-wikilink-index]');
        if (!option) return;
        state.wikilinkAutocomplete.activeIndex = Number(option.dataset.wikilinkIndex);
        updateWikilinkActiveOption();
    });
    elements.markdownPreview.addEventListener('click', event => {
        const frontmatterToggle = event.target.closest('[data-frontmatter-toggle]');
        if (frontmatterToggle) {
            state.frontmatterExpanded = !state.frontmatterExpanded;
            renderFrontmatterProperties();
            return;
        }
        const link = event.target.closest('a[href]');
        if (!link) return;
        const params = new URLSearchParams(link.getAttribute('href').replace(/^\?/, ''));
        const noteID = params.get('note');
        if (!/^\d+$/.test(noteID || '')) return;
        event.preventDefault();
        void openNote(Number(noteID));
    });
    elements.noteContent.addEventListener('scroll', () => {
        handleEditorScroll(elements.noteContent);
        if (state.wikilinkAutocomplete.open) renderWikilinkAutocomplete();
    });
    elements.markdownPreview.addEventListener('scroll', () => handleEditorScroll(elements.markdownPreview));
    window.addEventListener('resize', () => {
        editorLinePositionCache = null;
        if (state.view === 'editor' && window.matchMedia('(max-width: 700px)').matches &&
            elements.editorWorkspace.dataset.editorMode === 'split') {
            setEditorMode('edit');
        }
        syncPreviewMinimumHeight();
        syncEditorScrollFromSource();
        if (state.wikilinkAutocomplete.open) renderWikilinkAutocomplete();
    });
    elements.noteTitle.addEventListener('input', () => {
        updateEditorBreadcrumb();
        scheduleSave();
    });
    const splitter = document.querySelector('.editor-splitter');
    splitter.addEventListener('pointerdown', startSplitResize);
    splitter.addEventListener('pointermove', resizeSplit);
    splitter.addEventListener('pointerup', stopSplitResize);
    splitter.addEventListener('pointercancel', stopSplitResize);
    splitter.addEventListener('keydown', handleSplitResizeKey);
    document.querySelector('.markdown-tools').addEventListener('click', event => {
        const action = event.target.closest('[data-action]');
        if (action?.dataset.action === 'undo') markdownEditor.undo();
        if (action?.dataset.action === 'redo') markdownEditor.redo();
        const button = event.target.closest('[data-format]');
        if (button) markdownEditor.format(button.dataset.format);
    });
    document.querySelectorAll('[data-editor-mode]').forEach(button => {
        button.addEventListener('click', () => setEditorMode(button.dataset.editorMode));
    });
    window.addEventListener('popstate', event => void handlePopState(event));
    document.addEventListener('keydown', event => {
        if (event.key === '/' && state.view === 'list' && !isTypingTarget(event.target)) {
            event.preventDefault();
            (state.graphVisible ? document.querySelector('#graphSearch') : elements.searchInput).focus();
        }
        if ((event.ctrlKey || event.metaKey) && event.altKey && !event.shiftKey && state.view === 'editor') {
            const mobile = window.matchMedia('(max-width: 700px)').matches;
            const modes = mobile ? ['edit', 'preview'] : ['edit', 'split', 'preview'];
            let mode = null;
            if (event.code === 'Digit1') mode = 'edit';
            else if (event.code === 'Digit2') mode = mobile ? 'edit' : 'split';
            else if (event.code === 'Digit3') mode = 'preview';
            else if (event.key.toLowerCase() === 'v') {
                mode = modes[(modes.indexOf(elements.editorWorkspace.dataset.editorMode) + 1) % modes.length];
            }
            if (mode) {
                event.preventDefault();
                setEditorMode(mode);
            }
        }
        if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'n') {
            event.preventDefault();
            void openNewNote();
        }
        if (state.view === 'editor' && event.key === 'Escape' && !event.defaultPrevented) {
            if (elements.attachmentsDialog.open) elements.attachmentsDialog.close();
            else navigateBack();
        }
        if (state.view === 'list' && state.graphVisible && event.key === 'Escape' && !event.defaultPrevented) {
            closeGraphView();
        }
    });
}

function isTypingTarget(target) {
    return target instanceof HTMLElement &&
        (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName));
}

function captureListState() {
    return {
        query: elements.searchInput.value,
        mode: state.mode,
        folder: state.folder,
        offset: state.offset,
        graphVisible: state.graphVisible
    };
}

function updateListHistory() {
    if (state.view === 'list') {
        history.replaceState({ view: 'list', list: captureListState() }, '', location.href);
    }
}

async function openGraphView() {
    if (state.view === 'editor') {
        if (!(await flushSave())) {
            showMessage('Salve a nota antes de abrir o grafo.', 'error');
            return;
        }
        const listState = state.listState || captureListState();
        history.pushState({ view: 'list', list: { ...listState, graphVisible: true } }, '', location.pathname);
        state.view = 'list';
        state.noteID = null;
        resetBacklinks();
        resetOutline();
        state.directEntry = false;
        document.body.classList.remove('is-editor-view');
        elements.editorView.hidden = true;
        elements.newNote.hidden = false;
        restoreListState({ ...listState, graphVisible: false });
        void loadNotes();
    }
    if (state.view !== 'list') return;
    state.graphVisible = true;
    elements.searchView.hidden = true;
    elements.graphView.hidden = false;
    const graphButton = document.querySelector('#graphButton');
    graphButton.classList.add('is-active');
    graphButton.setAttribute('aria-pressed', 'true');
    updateListHistory();
    requestAnimationFrame(() => graphView.resize());
    if (state.graphData) {
        graphView.setGraph(state.graphData);
        updateGraphSummary(state.graphData);
        return;
    }

    const requestID = ++state.graphRequestID;
    const summary = document.querySelector('#graphSummary');
    summary.textContent = 'Carregando relações…';
    try {
        const graph = await requestJSON('/api/graph');
        if (requestID !== state.graphRequestID || !state.graphVisible) return;
        if (!Array.isArray(graph.nodes) || !Array.isArray(graph.edges)) {
            throw new Error('Resposta inválida da API do grafo.');
        }
        state.graphData = graph;
        graphView.setGraph(graph);
        updateGraphSummary(graph);
        requestAnimationFrame(() => graphView.fit());
    } catch (error) {
        if (requestID !== state.graphRequestID) return;
        summary.textContent = 'Não foi possível carregar as relações.';
        showMessage(`Não foi possível carregar o grafo: ${error.message}`, 'error');
    }
}

function closeGraphView() {
    if (!state.graphVisible) return;
    state.graphVisible = false;
    state.graphRequestID++;
    elements.graphView.hidden = true;
    elements.searchView.hidden = false;
    const graphButton = document.querySelector('#graphButton');
    graphButton.classList.remove('is-active');
    graphButton.setAttribute('aria-pressed', 'false');
    updateListHistory();
}

function updateGraphSummary(graph) {
    const nodeCount = graph.nodes.length;
    const edgeCount = graph.edges.length;
    document.querySelector('#graphSummary').textContent =
        `${nodeCount} ${nodeCount === 1 ? 'nota' : 'notas'} · ${edgeCount} ${edgeCount === 1 ? 'relação' : 'relações'}`;
}

function updateSearchModes() {
    document.querySelectorAll('[data-mode]').forEach(button => {
        const active = button.dataset.mode === state.mode;
        button.classList.toggle('is-active', active);
        button.setAttribute('aria-pressed', String(active));
    });
}

async function loadNotes() {
    const requestID = ++state.requestID;
    const query = elements.searchInput.value.trim();
    const params = new URLSearchParams({
        folder: state.folder,
        limit: String(pageSize),
        offset: String(state.offset)
    });
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
            return loadNotes();
        }
        state.notes = page.items;
        state.total = page.total;
        renderNotes(page);
    } catch (error) {
        if (requestID === state.requestID) showMessage(`Não foi possível carregar as notas: ${error.message}`, 'error');
    }
}

async function loadFolders() {
    try {
        state.folders = await requestJSON('/api/folders');
        if (!Array.isArray(state.folders)) throw new Error('resposta de pastas inválida');
        if (elements.panel.dataset.panel === 'files') renderFolderPanel();
    } catch (error) {
        showMessage(`Não foi possível carregar as pastas: ${error.message}`, 'error');
    }
}

async function loadFolderNotes(folder, more = false) {
    const previous = state.folderNotes.get(folder);
    if (!more && previous) return;
    if (state.folderNotesLoading.has(folder)) return;
    const generation = state.folderNotesGeneration;
    const offset = more && previous ? previous.items.length : 0;
    const params = new URLSearchParams({ folder, limit: String(treePageSize), offset: String(offset) });
    state.folderNotesLoading.add(folder);
    try {
        const page = await requestJSON(`/api/folder-notes?${params}`);
        if (generation !== state.folderNotesGeneration) return;
        const items = more && previous ? [...previous.items, ...page.items] : page.items;
        state.folderNotes.set(folder, { items, total: page.total });
        if (elements.panel.dataset.panel === 'files') renderFolderPanel();
    } catch (error) {
        showMessage(`Não foi possível carregar as notas da pasta: ${error.message}`, 'error');
    } finally {
        state.folderNotesLoading.delete(folder);
        if (generation !== state.folderNotesGeneration && state.expandedFolders.has(folder)) {
            void loadFolderNotes(folder);
        }
    }
}

function renderNotes(page) {
    elements.notesList.replaceChildren();
    const query = elements.searchInput.value.trim();
    elements.resultsTitle.textContent = query
        ? `Resultados para “${query}”`
        : state.folder ? state.folder.split('/').at(-1) : 'Todas as notas';
    elements.resultCount.textContent = `${page.total} ${page.total === 1 ? 'nota' : 'notas'}`;
    if (state.view === 'list') {
        document.querySelector('#currentFolder').textContent = state.folder || 'Todas as notas';
    }
    document.querySelector('#clearFolder').hidden = !state.folder;
    if (page.items.length === 0) {
        const empty = document.createElement('div');
        empty.className = 'empty-state';
        empty.textContent = query ? 'Nenhuma nota corresponde à pesquisa.' : 'Ainda não há notas. Crie uma para começar.';
        elements.notesList.append(empty);
    } else {
        const contentSearch = Boolean(query && state.mode === 'content');
        for (const note of page.items) elements.notesList.append(createNoteCard(note, contentSearch));
    }
    const first = page.total === 0 ? 0 : page.offset + 1;
    const last = Math.min(page.offset + page.items.length, page.total);
    elements.pagination.hidden = page.total <= page.limit;
    elements.pageStatus.textContent = `${first}–${last} de ${page.total}`;
    elements.previousPage.disabled = page.offset === 0;
    elements.nextPage.disabled = page.offset + page.limit >= page.total;
}

function createNoteCard(note, contentSearch) {
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
        appendHighlightedPreview(preview, note.preview || 'Nenhum trecho disponível.');
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
        const tag = document.createElement('span');
        tag.className = 'note-tag';
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
}

function appendHighlightedPreview(container, text) {
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
}

function handleNoteListClick(event) {
    const action = event.target.closest('[data-action]');
    const card = event.target.closest('[data-note-id]');
    if (!card || !action) return;
    const id = Number(card.dataset.noteId);
    if (action.dataset.action === 'open') void openNote(id);
    else if (action.dataset.action === 'delete') void deleteNote(id);
}

function scheduleWikilinkSuggestions() {
    const token = markdownEditor.getWikilinkToken();
    if (state.view !== 'editor' || !token) {
        closeWikilinkAutocomplete();
        return;
    }
    window.clearTimeout(state.wikilinkTimer);
    state.wikilinkAutocomplete.open = false;
    elements.wikilinkAutocomplete.hidden = true;
    elements.noteContent.setAttribute('aria-expanded', 'false');
    elements.noteContent.removeAttribute('aria-activedescendant');
    const requestID = ++state.wikilinkRequestID;
    state.wikilinkTimer = window.setTimeout(async () => {
        try {
            const query = token.query.trim();
            const params = new URLSearchParams({ limit: '100', offset: '0' });
            let url = `/api/notes?${params}`;
            if (query) {
                params.set('q', query);
                params.set('mode', 'title');
                url = `/api/search?${params}`;
            }
            const page = await requestJSON(url);
            const currentToken = markdownEditor.getWikilinkToken();
            if (requestID !== state.wikilinkRequestID || !currentToken ||
                currentToken.query !== token.query || currentToken.tokenStart !== token.tokenStart) return;

            const counts = new Map();
            for (const note of page.items) {
                const key = note.title.trim().toLocaleLowerCase();
                counts.set(key, (counts.get(key) || 0) + 1);
            }
            const normalizedQuery = query.toLocaleLowerCase();
            state.wikilinkAutocomplete.items = page.items
                .map(note => ({
                    label: note.title,
                    secondary: note.folder,
                    insert: counts.get(note.title.trim().toLocaleLowerCase()) > 1 && note.folder
                        ? `${note.folder}/${note.title}`
                        : note.title,
                    prefix: note.title.toLocaleLowerCase().startsWith(normalizedQuery),
                    updated: note.updated_at || ''
                }))
                .sort((a, b) => Number(b.prefix) - Number(a.prefix) ||
                    b.updated.localeCompare(a.updated) || a.label.localeCompare(b.label))
                .slice(0, 8);
            state.wikilinkAutocomplete.activeIndex = 0;
            state.wikilinkAutocomplete.open = state.wikilinkAutocomplete.items.length > 0;
            renderWikilinkAutocomplete();
        } catch (error) {
            if (requestID === state.wikilinkRequestID) {
                closeWikilinkAutocomplete();
                showMessage(`Não foi possível buscar notas para o link: ${error.message}`, 'error');
            }
        }
    }, 100);
}

function renderWikilinkAutocomplete() {
    const popup = elements.wikilinkAutocomplete;
    popup.replaceChildren();
    state.wikilinkAutocomplete.items.forEach((item, index) => {
        const option = document.createElement('button');
        option.type = 'button';
        option.className = 'wikilink-option';
        option.dataset.wikilinkIndex = String(index);
        option.id = `wikilink-option-${index}`;
        option.setAttribute('role', 'option');
        option.setAttribute('aria-selected', String(index === state.wikilinkAutocomplete.activeIndex));
        if (index === state.wikilinkAutocomplete.activeIndex) option.classList.add('is-active');
        const label = document.createElement('span');
        label.className = 'wikilink-option-label';
        label.textContent = item.label;
        option.append(label);
        if (item.secondary) {
            const folder = document.createElement('span');
            folder.className = 'wikilink-option-folder';
            folder.textContent = item.secondary;
            option.append(folder);
        }
        popup.append(option);
    });
    popup.hidden = !state.wikilinkAutocomplete.open;
    elements.noteContent.setAttribute('aria-expanded', String(state.wikilinkAutocomplete.open));
    if (state.wikilinkAutocomplete.open) {
        elements.noteContent.setAttribute(
            'aria-activedescendant',
            `wikilink-option-${state.wikilinkAutocomplete.activeIndex}`
        );
        const position = getTextareaCaretPosition(elements.noteContent, elements.noteContent.selectionStart);
        const bounds = popup.getBoundingClientRect();
        const left = Math.min(position.left, window.innerWidth - bounds.width - 8);
        const top = position.top + bounds.height > window.innerHeight
            ? Math.max(8, position.top - position.height - bounds.height)
            : position.top;
        popup.style.left = `${Math.max(8, left)}px`;
        popup.style.top = `${Math.max(8, top)}px`;
    } else {
        elements.noteContent.removeAttribute('aria-activedescendant');
    }
}

function getTextareaCaretPosition(textarea, caret) {
    const computed = window.getComputedStyle(textarea);
    const mirror = document.createElement('div');
    const style = mirror.style;
    for (const property of [
        'boxSizing', 'width', 'borderTopWidth', 'borderRightWidth', 'borderBottomWidth',
        'borderLeftWidth', 'borderStyle', 'paddingTop', 'paddingRight', 'paddingBottom',
        'paddingLeft', 'fontStyle', 'fontVariant', 'fontWeight', 'fontStretch', 'fontSize',
        'fontFamily', 'textAlign', 'textTransform', 'textIndent', 'letterSpacing',
        'wordSpacing', 'lineHeight', 'tabSize'
    ]) style[property] = computed[property];
    style.position = 'absolute';
    style.visibility = 'hidden';
    style.whiteSpace = 'pre-wrap';
    style.wordWrap = 'break-word';
    style.overflowWrap = 'break-word';
    style.height = 'auto';
    style.left = '-100000px';
    style.top = '0';
    mirror.textContent = textarea.value.slice(0, caret);
    const marker = document.createElement('span');
    marker.textContent = textarea.value.slice(caret) || '.';
    mirror.append(marker);
    document.body.append(mirror);
    const lineHeight = parseFloat(computed.lineHeight) || parseFloat(computed.fontSize) * 1.2;
    const top = marker.offsetTop + (parseFloat(computed.borderTopWidth) || 0);
    const left = marker.offsetLeft + (parseFloat(computed.borderLeftWidth) || 0);
    mirror.remove();
    const bounds = textarea.getBoundingClientRect();
    return {
        top: bounds.top + top - textarea.scrollTop + lineHeight,
        left: bounds.left + left - textarea.scrollLeft,
        height: lineHeight
    };
}

function handleWikilinkKeydown(event) {
    if (!state.wikilinkAutocomplete.open) return false;
    const { items, activeIndex } = state.wikilinkAutocomplete;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        event.stopImmediatePropagation();
        state.wikilinkAutocomplete.activeIndex =
            (activeIndex + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
        renderWikilinkAutocomplete();
        return true;
    }
    if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault();
        event.stopImmediatePropagation();
        if (items[activeIndex]) acceptWikilinkSuggestion(items[activeIndex]);
        return true;
    }
    if (event.key === 'Escape') {
        event.preventDefault();
        event.stopImmediatePropagation();
        closeWikilinkAutocomplete();
        return true;
    }
    if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) closeWikilinkAutocomplete();
    return false;
}

function acceptWikilinkSuggestion(item) {
    closeWikilinkAutocomplete();
    markdownEditor.acceptWikilink(item.insert);
}

function updateWikilinkActiveOption() {
    for (const [index, option] of [...elements.wikilinkAutocomplete.children].entries()) {
        const active = index === state.wikilinkAutocomplete.activeIndex;
        option.classList.toggle('is-active', active);
        option.setAttribute('aria-selected', String(active));
    }
}

function closeWikilinkAutocomplete() {
    window.clearTimeout(state.wikilinkTimer);
    state.wikilinkRequestID++;
    state.wikilinkAutocomplete.open = false;
    state.wikilinkAutocomplete.items = [];
    elements.wikilinkAutocomplete.hidden = true;
    elements.noteContent.setAttribute('aria-expanded', 'false');
    elements.noteContent.removeAttribute('aria-activedescendant');
}

async function openNewNote() {
    if (state.view === 'editor' && !(await flushSave())) return;
    if (state.view === 'list') state.listState = captureListState();
    history.pushState({ view: 'editor', noteId: null, list: state.listState, direct: false }, '', location.pathname);
    state.directEntry = false;
    showEditor(null);
}

async function openNote(id, { push = true } = {}) {
    if (state.view === 'editor' && !(await flushSave())) return;
    const navigationID = ++state.navigationID;
    let listState = state.view === 'editor' ? state.listState : captureListState();
    try {
        const note = await requestJSON(`/api/notes/${id}`);
        if (navigationID !== state.navigationID) return;
        state.listState = listState;
        if (push) {
            history.pushState({ view: 'editor', noteId: id, list: listState, direct: false }, '', `?note=${id}`);
            state.directEntry = false;
        }
        showEditor(note);
    } catch (error) {
        showMessage(`Não foi possível abrir a nota: ${error.message}`, 'error');
    }
}

function showEditor(note) {
    window.clearTimeout(state.saveTimer);
    closeWikilinkAutocomplete();
    elements.attachmentsDialog.close();
    if (window.matchMedia('(max-width: 700px)').matches) closePanel();
    document.body.classList.add('is-editor-view');
    state.view = 'editor';
    state.noteID = note?.id ?? null;
    elements.searchView.hidden = true;
    elements.graphView.hidden = true;
    elements.editorView.hidden = false;
    elements.newNote.hidden = true;
    elements.noteTitle.value = note?.title || '';
    state.frontmatterExpanded = false;
    state.noteFolder = note ? note.folder : state.folder;
    elements.deleteCurrentNote.hidden = !note;
    elements.attachmentList.replaceChildren();
    elements.attachmentList.hidden = true;
    elements.attachmentsEmpty.hidden = false;
    elements.attachmentsButton.title = 'Abrir anexos';
    elements.attachmentsButton.setAttribute('aria-label', 'Abrir anexos');
    resetBacklinks();
    elements.saveStatus.className = 'save-status';
    elements.saveStatus.textContent = note ? 'Salvo' : 'Rascunho';
    const content = note ? ensureFrontmatterTags(note.content, note.tags || []) : '';
    state.outline = extractMarkdownOutline(content);
    updateOutlineCount();
    renderedPreviewMarkdown = '';
    editorLinePositionCache = null;
    elements.noteContent.value = content;
    state.savedDraft = getDraft();
    markdownEditor.setValue(content);
    const savedMode = localStorage.getItem('editorMode');
    const preferredMode = ['edit', 'split', 'preview'].includes(savedMode) ? savedMode : 'split';
    setEditorMode(window.matchMedia('(max-width: 700px)').matches && preferredMode === 'split'
        ? 'edit'
        : preferredMode);
    updatePreview();
    elements.noteContent.focus();
    updateEditorBreadcrumb();
    if (note) {
        void loadAttachments(note.id);
        void loadBacklinks(note.id);
    } else if (elements.panel.dataset.panel === 'backlinks') {
        renderBacklinksPanel();
    } else if (elements.panel.dataset.panel === 'outline') {
        renderOutlinePanel();
    }
}

function updateEditorBreadcrumb() {
    elements.currentFolder.textContent = state.noteFolder || 'Sem pasta';
    elements.noteTitle.title = elements.noteTitle.value || 'Título da nota';
}

function getDraft() {
    return {
        title: elements.noteTitle.value,
        folder: state.noteFolder,
        content: elements.noteContent.value
    };
}

function isDraftDirty(draft = getDraft()) {
    return JSON.stringify(draft) !== JSON.stringify(state.savedDraft);
}

function scheduleSave() {
    if (state.view !== 'editor') return;
    if (!isDraftDirty()) {
        setSaveStatus(state.noteID ? 'Salvo' : 'Rascunho', '');
        return;
    }
    setSaveStatus(state.savePromise ? 'Salvando…' : 'Alterações pendentes', 'pending');
    window.clearTimeout(state.saveTimer);
    state.saveTimer = window.setTimeout(() => void persistDraft(), 650);
}

async function persistDraft() {
    if (state.savePromise) {
        await state.savePromise;
        return isDraftDirty() ? persistDraft() : true;
    }
    const draft = getDraft();
    if (!isDraftDirty(draft)) return true;
    const note = {
        title: draft.title.trim(),
        folder: draft.folder,
        content: draft.content
    };
    if (!note.title) {
        setSaveStatus('Informe um título para salvar', 'error');
        return false;
    }
    const id = state.noteID;
    const endpoint = id === null ? '/api/notes' : `/api/notes/${id}`;
    const method = id === null ? 'POST' : 'PUT';
    setSaveStatus('Salvando…', 'pending');
    const job = (async () => {
        try {
            const saved = await requestJSON(endpoint, { method, body: JSON.stringify(note) });
            state.graphData = null;
            if (id === null) {
                state.noteID = saved.id;
                updatePreview();
                elements.deleteCurrentNote.hidden = false;
                void loadAttachments(saved.id);
                const current = history.state || {};
                history.replaceState({ ...current, view: 'editor', noteId: saved.id, list: state.listState }, '', `?note=${saved.id}`);
            }
            void loadBacklinks(state.noteID);
            if (JSON.stringify(getDraft()) === JSON.stringify(draft)) {
                elements.noteTitle.value = saved.title;
                state.noteFolder = saved.folder;
                state.savedDraft = getDraft();
                updateEditorBreadcrumb();
            } else {
                state.savedDraft = draft;
            }
            updateEditorBreadcrumb();
            setSaveStatus('Salvo automaticamente', 'saved');
            state.folderNotesGeneration++;
            state.folderNotes.clear();
            void Promise.all([loadFolders(), loadNotes()]);
            void loadFolderNotes('', false);
            return true;
        } catch (error) {
            setSaveStatus(`Falha ao salvar: ${error.message}`, 'error');
            return false;
        }
    })();
    state.savePromise = job;
    const result = await job;
    state.savePromise = null;
    if (result && isDraftDirty()) {
        window.clearTimeout(state.saveTimer);
        state.saveTimer = window.setTimeout(() => void persistDraft(), 0);
    }
    return result;
}

async function flushSave() {
    window.clearTimeout(state.saveTimer);
    while (state.savePromise) {
        await state.savePromise;
        if (state.savePromise) continue;
    }
    if (!isDraftDirty()) return true;
    if (!elements.noteTitle.value.trim()) {
        setSaveStatus('Informe um título antes de sair', 'error');
        elements.noteTitle.focus();
        return false;
    }
    const success = await persistDraft();
    if (!success) return false;
    if (isDraftDirty()) return flushSave();
    return true;
}

function setSaveStatus(message, stateName) {
    elements.saveStatus.textContent = message;
    elements.saveStatus.dataset.state = stateName;
    elements.saveStatus.className = `save-status${stateName ? ` is-${stateName}` : ''}`;
}

async function loadAttachments(noteID) {
    try {
        const attachments = await requestJSON(`/api/notes/${noteID}/attachments`);
        if (state.noteID === noteID) renderAttachments(attachments, noteID);
    } catch (error) {
        if (state.noteID === noteID) showMessage(`Não foi possível carregar os anexos: ${error.message}`, 'error');
    }
}

function renderAttachments(attachments, noteID) {
    const list = elements.attachmentList;
    list.replaceChildren();
    for (const attachment of attachments) {
        const item = document.createElement('li');
        const link = document.createElement('a');
        link.href = attachment.url;
        link.textContent = `${attachment.filename} (${formatFileSize(attachment.size)})`;
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
    elements.attachmentsEmpty.hidden = attachments.length !== 0;
    const label = attachments.length ? `Anexos (${attachments.length})` : 'Abrir anexos';
    elements.attachmentsButton.title = label;
    elements.attachmentsButton.setAttribute('aria-label', label);
}

function formatFileSize(size) {
    if (size < 1024) return `${size} B`;
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
    return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

async function uploadAttachments(files) {
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
                insertAttachmentMarkdown(attachment);
                await loadAttachments(noteID);
            }
        } catch (error) {
            showMessage(`Não foi possível enviar ${file.name}: ${error.message}`, 'error');
            return;
        }
    }
    if (state.noteID === noteID) await flushSave();
}

function insertAttachmentMarkdown(attachment) {
    const textarea = elements.noteContent;
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
}

async function deleteAttachment(attachmentID) {
    const noteID = state.noteID;
    if (noteID === null) return;
    const button = document.querySelector(`[data-delete-attachment="${attachmentID}"]`);
    const filename = button?.previousElementSibling?.textContent || 'este anexo';
    if (!window.confirm(`Excluir ${filename}? Essa ação não pode ser desfeita.`)) return;
    try {
        await requestJSON(`/api/notes/${noteID}/attachments/${attachmentID}`, { method: 'DELETE' });
        await loadAttachments(noteID);
        showMessage('Anexo excluído.', 'success');
    } catch (error) {
        showMessage(`Não foi possível excluir o anexo: ${error.message}`, 'error');
    }
}

function navigateBack() {
    if (state.view !== 'editor') return;
    if (state.directEntry) {
        void returnToList(state.listState || captureListState(), true);
    } else {
        history.back();
    }
}

async function returnToList(listState, replace = false) {
    if (!(await flushSave())) return;
    const target = listState || { query: '', mode: 'title', folder: '', offset: 0 };
    if (replace) history.replaceState({ view: 'list', list: target }, '', location.pathname);
    state.view = 'list';
    state.noteID = null;
    resetBacklinks();
    resetOutline();
    document.body.classList.remove('is-editor-view');
    state.directEntry = false;
    restoreListState(target);
    elements.editorView.hidden = true;
    elements.graphView.hidden = !state.graphVisible;
    elements.searchView.hidden = state.graphVisible;
    elements.newNote.hidden = false;
    await loadNotes();
    if (elements.panel.dataset.panel === 'search') elements.searchInput.focus();
}

function restoreListState(listState) {
    elements.searchInput.value = listState.query || '';
    state.mode = listState.mode || 'title';
    state.folder = listState.folder || '';
    state.offset = listState.offset || 0;
    state.graphVisible = Boolean(listState.graphVisible);
    elements.graphView.hidden = !state.graphVisible;
    elements.searchView.hidden = state.graphVisible;
    const graphButton = document.querySelector('#graphButton');
    graphButton.classList.toggle('is-active', state.graphVisible);
    graphButton.setAttribute('aria-pressed', String(state.graphVisible));
    if (state.graphVisible && state.graphData) {
        graphView.setGraph(state.graphData);
        updateGraphSummary(state.graphData);
        requestAnimationFrame(() => graphView.resize());
    } else if (state.graphVisible) {
        void openGraphView();
    }
    updateSearchModes();
    document.querySelector('#currentFolder').textContent = state.folder || 'Todas as notas';
}

async function handlePopState(event) {
    const target = event.state || { view: 'list', list: state.listState };
    if (state.view === 'editor' && !(await flushSave())) {
        history.forward();
        return;
    }
    if (target.view === 'editor' && target.noteId) {
        try {
            const note = await requestJSON(`/api/notes/${target.noteId}`);
            state.listState = target.list || state.listState || captureListState();
            state.directEntry = false;
            showEditor(note);
        } catch (error) {
            showMessage(`Não foi possível abrir a nota: ${error.message}`, 'error');
            history.forward();
        }
        return;
    }
    if (target.view === 'editor') {
        state.listState = target.list || state.listState || captureListState();
        state.directEntry = Boolean(target.direct);
        showEditor(null);
        return;
    }
    state.listState = target.list || state.listState;
    state.directEntry = false;
    state.view = 'list';
    document.body.classList.remove('is-editor-view');
    restoreListState(state.listState || {});
    state.noteID = null;
    resetBacklinks();
    resetOutline();
    elements.editorView.hidden = true;
    elements.graphView.hidden = !state.graphVisible;
    elements.searchView.hidden = state.graphVisible;
    elements.newNote.hidden = false;
    await loadNotes();
    if (elements.panel.dataset.panel === 'search') elements.searchInput.focus();
}

async function deleteCurrentNote() {
    if (state.noteID === null) return;
    if (!window.confirm(`Excluir “${elements.noteTitle.value}”? Essa ação não pode ser desfeita.`)) return;
    try {
        window.clearTimeout(state.saveTimer);
        if (state.savePromise) await state.savePromise;
        await requestJSON(`/api/notes/${state.noteID}`, { method: 'DELETE' });
        state.graphData = null;
        state.savedDraft = getDraft();
        showMessage('Nota excluída.', 'success');
        state.noteID = null;
        resetBacklinks();
        resetOutline();
        state.folderNotesGeneration++;
        state.folderNotes.clear();
        navigateBack();
        void Promise.all([loadFolders(), loadNotes()]);
        void loadFolderNotes('', false);
    } catch (error) {
        setSaveStatus(`Não foi possível excluir: ${error.message}`, 'error');
    }
}

async function deleteNote(id) {
    const note = state.notes.find(item => item.id === id) ||
        [...state.folderNotes.values()].flatMap(page => page.items).find(item => item.id === id);
    if (!window.confirm(`Excluir “${note?.title || 'esta nota'}”? Essa ação não pode ser desfeita.`)) return;
    const deletingCurrent = state.view === 'editor' && state.noteID === id;
    try {
        if (deletingCurrent) {
            window.clearTimeout(state.saveTimer);
            if (state.savePromise) await state.savePromise;
        }
        await requestJSON(`/api/notes/${id}`, { method: 'DELETE' });
        state.graphData = null;
        showMessage('Nota excluída.', 'success');
        state.folderNotesGeneration++;
        state.folderNotes.clear();
        if (deletingCurrent) {
            state.savedDraft = getDraft();
            state.noteID = null;
            resetBacklinks();
            resetOutline();
            if (state.directEntry) await returnToList(state.listState || captureListState(), true);
            else history.back();
            void Promise.all([loadFolders(), loadNotes()]);
            void loadFolderNotes('', false);
            return;
        }
        state.total = Math.max(0, state.total - 1);
        if (state.offset > 0 && state.offset >= state.total) state.offset = Math.max(0, state.offset - pageSize);
        await Promise.all([loadNotes(), loadFolders()]);
        await loadFolderNotes('', false);
    } catch (error) {
        showMessage(`Não foi possível excluir a nota: ${error.message}`, 'error');
    }
}

function updatePreview() {
    const requestID = ++previewRequestID;
    window.clearTimeout(previewTimer);
    const markdown = stripFrontmatter(elements.noteContent.value);
    if (!markdown.trim()) {
        elements.markdownPreview.textContent = '';
        renderFrontmatterProperties();
        renderedPreviewMarkdown = markdown;
        editorLinePositionCache = null;
        syncPreviewMinimumHeight();
        syncEditorScrollFromSource();
        return;
    }
    previewTimer = window.setTimeout(async () => {
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
                location.replace('/login.html');
                return;
            }
            if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
            const html = await response.text();
            if (requestID === previewRequestID) {
                elements.markdownPreview.innerHTML = html;
                renderFrontmatterProperties();
                syncPreviewMinimumHeight();
                renderedPreviewMarkdown = markdown;
                annotatePreviewLandmarks(markdown);
                syncEditorScrollFromSource();
                if (state.pendingOutlineLine !== null) {
                    const pendingLine = state.pendingOutlineLine;
                    state.pendingOutlineLine = null;
                    requestAnimationFrame(() => scrollToOutlineHeading(pendingLine));
                }
            }
        } catch (error) {
            if (requestID === previewRequestID) {
                elements.markdownPreview.textContent = `Falha ao renderizar Markdown: ${error.message}`;
                renderedPreviewMarkdown = '';
                renderFrontmatterProperties();
                syncPreviewMinimumHeight();
                syncEditorScrollFromSource();
            }
        }
    }, 140);
}

function renderFrontmatterProperties() {
    elements.markdownPreview.querySelector('.frontmatter-properties')?.remove();
    const metadata = parseFrontmatter(elements.noteContent.value);
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
    elements.markdownPreview.prepend(panel);
}

function syncPreviewMinimumHeight() {
    const preview = elements.markdownPreview;
    let spacer = preview.querySelector(':scope > .preview-scroll-spacer');
    if (elements.noteContent.clientHeight === 0 || preview.clientHeight === 0) {
        spacer?.remove();
        return;
    }
    const contentEnd = spacer ? spacer.previousElementSibling : preview.lastElementChild;
    const contentHeight = contentEnd
        ? Math.max(0, contentEnd.getBoundingClientRect().bottom - preview.getBoundingClientRect().top + preview.scrollTop)
        : 0;
    const neededHeight = getScrollSpacerHeight(
        Math.max(0, elements.noteContent.scrollHeight - elements.noteContent.clientHeight),
        contentHeight,
        preview.clientHeight
    );
    if (neededHeight > 0 && !spacer) {
        spacer = document.createElement('div');
        spacer.className = 'preview-scroll-spacer';
        preview.append(spacer);
    }
    if (spacer) {
        if (neededHeight > 0) spacer.style.height = `${neededHeight}px`;
        else spacer.remove();
    }
}

function scanMarkdownLandmarks(markdown) {
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
}

function previewBlockKind(element) {
    if (/^H[1-6]$/.test(element.tagName)) return element.tagName.toLowerCase();
    if (element.tagName === 'PRE') return 'pre';
    if (element.tagName === 'HR') return 'hr';
    if (element.tagName === 'TABLE') return 'table';
    if (element.tagName === 'IMG' ||
        (element.tagName === 'P' && element.childNodes.length === 1 && element.firstElementChild?.tagName === 'IMG')) return 'img';
    return null;
}

function annotatePreviewLandmarks(markdown) {
    const byKind = new Map();
    for (const landmark of scanMarkdownLandmarks(markdown)) {
        if (!byKind.has(landmark.kind)) byKind.set(landmark.kind, []);
        byKind.get(landmark.kind).push(landmark);
    }
    const previewBlocks = [...elements.markdownPreview.children].map(element => ({
        element,
        kind: previewBlockKind(element)
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
}

function measureEditorLines(markdown, lines) {
    const editor = elements.noteContent;
    const computed = getComputedStyle(editor);
    const cacheKey = [
        markdown, editor.clientWidth, computed.fontFamily, computed.fontSize,
        computed.fontWeight, computed.lineHeight, computed.letterSpacing,
        computed.tabSize, computed.padding, [...lines].sort((a, b) => a - b).join(',')
    ].join('|');
    if (editorLinePositionCache?.key === cacheKey) return editorLinePositionCache.positions;

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
    editorLinePositionCache = { key: cacheKey, positions };
    return positions;
}

function getScrollLandmarks() {
    const markdown = stripFrontmatter(elements.noteContent.value);
    if (renderedPreviewMarkdown !== markdown) return [];
    const editor = elements.noteContent;
    const preview = elements.markdownPreview;
    const editorScrollable = Math.max(0, editor.scrollHeight - editor.clientHeight);
    const previewScrollable = Math.max(0, preview.scrollHeight - preview.clientHeight);
    if (!editorScrollable || !previewScrollable) return [];
    const sourceLines = [...elements.markdownPreview.querySelectorAll('[data-source-line]')]
        .map(element => Number(element.dataset.sourceLine));
    const positions = measureEditorLines(markdown, sourceLines);
    const previewRect = preview.getBoundingClientRect();
    return [...elements.markdownPreview.querySelectorAll('[data-source-line]')]
        .map(element => {
            const sourceY = positions.get(Number(element.dataset.sourceLine));
            const previewTop = element.getBoundingClientRect().top - previewRect.top + preview.scrollTop;
            return {
                editorPct: Number.isFinite(sourceY) ? sourceY / editorScrollable : NaN,
                previewTop
            };
        })
        .filter(anchor => anchor.editorPct > 0 && anchor.editorPct < 1 &&
            anchor.previewTop > 0 && anchor.previewTop < previewScrollable);
}

function handleEditorScroll(source) {
    if (programmaticScroll?.element === source) {
        const matched = Math.abs(source.scrollTop - programmaticScroll.scrollTop) < 1;
        programmaticScroll = null;
        if (matched) return;
    }
    pendingScrollSource = source;
    if (scrollSyncFrame) return;
    scrollSyncFrame = requestAnimationFrame(() => {
        scrollSyncFrame = 0;
        const source = pendingScrollSource;
        pendingScrollSource = null;
        if (!source) return;
        const landmarks = getScrollLandmarks();
        const fromEditor = source === elements.noteContent;
        const sourceScrollable = Math.max(0, source.scrollHeight - source.clientHeight);
        const target = fromEditor ? elements.markdownPreview : elements.noteContent;
        const targetScrollable = Math.max(0, target.scrollHeight - target.clientHeight);
        const targetTop = fromEditor
            ? mapEditorToPreview(source.scrollTop, sourceScrollable, targetScrollable, landmarks)
            : mapPreviewToEditor(source.scrollTop, sourceScrollable, targetScrollable, landmarks);
        if (Math.abs(target.scrollTop - targetTop) >= 1) {
            programmaticScroll = { element: target, scrollTop: targetTop };
            target.scrollTop = targetTop;
        }
    });
}

function syncEditorScrollFromSource() {
    const editor = elements.noteContent;
    const preview = elements.markdownPreview;
    const targetTop = mapEditorToPreview(
        editor.scrollTop,
        Math.max(0, editor.scrollHeight - editor.clientHeight),
        Math.max(0, preview.scrollHeight - preview.clientHeight),
        getScrollLandmarks()
    );
    if (Math.abs(preview.scrollTop - targetTop) >= 1) {
        programmaticScroll = { element: preview, scrollTop: targetTop };
        preview.scrollTop = targetTop;
    }
}

function updateEditorHistoryButtons({ canUndo, canRedo }) {
    document.querySelector('[data-action="undo"]').disabled = !canUndo;
    document.querySelector('[data-action="redo"]').disabled = !canRedo;
}

function setEditorMode(mode) {
    if (mode === 'split' && window.matchMedia('(max-width: 700px)').matches) mode = 'edit';
    elements.editorWorkspace.dataset.editorMode = mode;
    localStorage.setItem('editorMode', mode);
    document.querySelectorAll('[data-editor-mode]').forEach(button => {
        const active = button.dataset.editorMode === mode;
        button.classList.toggle('is-active', active);
        button.setAttribute('aria-pressed', String(active));
    });
    requestAnimationFrame(() => {
        syncPreviewMinimumHeight();
        syncEditorScrollFromSource();
    });
}

function togglePanel(name) {
    if (elements.panel.dataset.panel === name && document.body.classList.contains('panel-open')) {
        closePanel();
        return;
    }
    elements.panel.dataset.panel = name;
    document.body.classList.add('panel-open');
    document.querySelectorAll('.rail-button[data-panel]').forEach(button => button.classList.toggle('is-active', button.dataset.panel === name));
    if (name === 'backlinks') {
        elements.panelTitle.textContent = 'Backlinks';
        renderBacklinksPanel();
        if (state.noteID !== null && state.backlinks === null) void loadBacklinks(state.noteID);
    } else if (name === 'outline') {
        elements.panelTitle.textContent = 'Sumário';
        renderOutlinePanel();
    } else {
        elements.panelTitle.textContent = 'Pastas e notas';
        renderFolderPanel();
    }
}

function getWorkspacePanelWidthBounds() {
    const max = Math.max(220, Math.min(640, window.innerWidth - 58 - 80));
    return { min: Math.min(220, max), max };
}

function setWorkspacePanelWidth(width, persist = false) {
    const { min, max } = getWorkspacePanelWidthBounds();
    workspacePanelWidth = Math.round(Math.min(max, Math.max(min, width)));
    document.documentElement.style.setProperty('--workspace-panel-width', `${workspacePanelWidth}px`);
    elements.panelResizer.setAttribute('aria-valuemax', String(max));
    elements.panelResizer.setAttribute('aria-valuenow', String(workspacePanelWidth));
    if (persist) localStorage.setItem('workspacePanelWidth', String(workspacePanelWidth));
}

function startWorkspacePanelResize(event) {
    if (event.button !== 0 || window.matchMedia('(max-width: 700px)').matches) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    document.body.classList.add('is-resizing-workspace-panel');
    resizeWorkspacePanel(event);
}

function resizeWorkspacePanel(event) {
    if (!event.currentTarget.hasPointerCapture?.(event.pointerId)) return;
    setWorkspacePanelWidth(event.clientX - 58);
}

function stopWorkspacePanelResize(event) {
    if (!event.currentTarget.hasPointerCapture?.(event.pointerId)) return;
    event.currentTarget.releasePointerCapture(event.pointerId);
    document.body.classList.remove('is-resizing-workspace-panel');
    setWorkspacePanelWidth(workspacePanelWidth, true);
}

function handleWorkspacePanelResizeKey(event) {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const { min, max } = getWorkspacePanelWidthBounds();
    const width = event.key === 'Home' ? min
        : event.key === 'End' ? max
            : workspacePanelWidth + (event.key === 'ArrowRight' ? 20 : -20);
    setWorkspacePanelWidth(width, true);
}

function closePanel() {
    document.body.classList.remove('panel-open');
    document.querySelectorAll('.rail-button[data-panel]').forEach(button => button.classList.remove('is-active'));
}

async function loadBacklinks(noteID) {
    const requestID = ++state.backlinksRequestID;
    state.backlinks = null;
    state.backlinksError = '';
    updateBacklinksCount();
    if (elements.panel.dataset.panel === 'backlinks') renderBacklinksPanel();
    try {
        const backlinks = await requestJSON(`/api/notes/${noteID}/backlinks`);
        if (requestID !== state.backlinksRequestID || state.noteID !== noteID) return;
        if (!Array.isArray(backlinks)) throw new Error('resposta de backlinks inválida');
        state.backlinks = backlinks;
        state.backlinksError = '';
        updateBacklinksCount();
        if (elements.panel.dataset.panel === 'backlinks') renderBacklinksPanel();
    } catch (error) {
        if (requestID !== state.backlinksRequestID || state.noteID !== noteID) return;
        state.backlinks = null;
        state.backlinksError = error.message;
        updateBacklinksCount();
        if (elements.panel.dataset.panel === 'backlinks') renderBacklinksPanel('Não foi possível carregar os backlinks.');
        showMessage(`Não foi possível carregar os backlinks: ${error.message}`, 'error');
    }
}

function updateBacklinksCount() {
    const count = state.view === 'editor' && state.noteID !== null ? state.backlinks?.length || 0 : 0;
    elements.backlinksCount.textContent = count > 9 ? '9+' : String(count);
    elements.backlinksCount.hidden = count === 0;
    const label = count ? `Backlinks (${count})` : 'Abrir backlinks';
    document.querySelector('#backlinksButton').title = label;
    document.querySelector('#backlinksButton').setAttribute('aria-label', label);
}

function resetBacklinks() {
    state.backlinks = null;
    state.backlinksError = '';
    state.backlinksRequestID++;
    updateBacklinksCount();
    if (elements.panel.dataset.panel === 'backlinks') renderBacklinksPanel();
}

function renderBacklinksPanel(errorMessage = '') {
    elements.panelContent.replaceChildren();
    if (state.view !== 'editor' || state.noteID === null) {
        const empty = document.createElement('div');
        empty.className = 'backlinks-panel-empty';
        const message = document.createElement('p');
        message.textContent = 'Abra uma nota para ver os backlinks.';
        empty.append(message);
        elements.panelContent.append(empty);
        return;
    }
    if (state.backlinksError || errorMessage) {
        const error = document.createElement('div');
        error.className = 'backlinks-panel-empty';
        const message = document.createElement('p');
        message.textContent = errorMessage || state.backlinksError;
        error.append(message);
        elements.panelContent.append(error);
        return;
    }
    if (state.backlinks === null) {
        const loading = document.createElement('div');
        loading.className = 'backlinks-panel-empty';
        loading.textContent = 'Carregando backlinks…';
        elements.panelContent.append(loading);
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
        elements.panelContent.append(empty);
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
        elements.panelContent.append(item);
    }
}

function refreshOutline() {
    state.outline = extractMarkdownOutline(elements.noteContent.value);
    updateOutlineCount();
    if (elements.panel.dataset.panel === 'outline') renderOutlinePanel();
}

function updateOutlineCount() {
    const count = state.view === 'editor' ? state.outline.length : 0;
    elements.outlineCount.textContent = count > 9 ? '9+' : String(count);
    elements.outlineCount.hidden = count === 0;
    const label = count ? `Sumário (${count})` : 'Abrir sumário da nota';
    document.querySelector('#outlineButton').title = label;
    document.querySelector('#outlineButton').setAttribute('aria-label', label);
}

function resetOutline() {
    state.outline = [];
    updateOutlineCount();
    if (elements.panel.dataset.panel === 'outline') renderOutlinePanel();
}

function renderOutlinePanel() {
    elements.panelContent.replaceChildren();
    if (state.view !== 'editor') {
        const empty = document.createElement('div');
        empty.className = 'outline-panel-empty';
        const message = document.createElement('p');
        message.textContent = 'Abra uma nota para ver o sumário.';
        empty.append(message);
        elements.panelContent.append(empty);
        return;
    }
    if (state.outline.length === 0) {
        const empty = document.createElement('div');
        empty.className = 'outline-panel-empty';
        const message = document.createElement('p');
        message.textContent = 'Esta nota não tem títulos.';
        empty.append(message);
        elements.panelContent.append(empty);
        return;
    }
    for (const heading of state.outline) {
        const item = document.createElement('button');
        item.type = 'button';
        item.className = 'outline-item';
        item.dataset.outlineLine = String(heading.line);
        item.dataset.level = String(heading.level);
        item.style.paddingLeft = `${10 + (heading.level - 1) * 12}px`;
        item.textContent = heading.text;
        item.title = heading.text;
        elements.panelContent.append(item);
    }
}

function scrollToOutlineHeading(line) {
    const lineIndex = Number(line);
    const heading = state.outline.find(item => item.line === lineIndex);
    if (!heading) return;
    const mode = elements.editorWorkspace.dataset.editorMode;
    if (mode !== 'edit' && renderedPreviewMarkdown !== stripFrontmatter(elements.noteContent.value)) {
        state.pendingOutlineLine = lineIndex;
        updatePreview();
        return;
    }
    if (mode !== 'preview') {
        const markdown = elements.noteContent.value;
        const lines = markdown.split('\n');
        const lineTop = measureEditorLines(markdown, [heading.line]).get(heading.line);
        let caret = 0;
        for (let index = 0; index < heading.line; index++) caret += lines[index].length + 1;
        elements.noteContent.focus();
        elements.noteContent.setSelectionRange(caret, caret);
        requestAnimationFrame(() => {
            if (!Number.isFinite(lineTop)) return;
            elements.noteContent.scrollTop = Math.max(0, lineTop - elements.noteContent.clientHeight / 3);
            handleEditorScroll(elements.noteContent);
        });
    }

    if (mode !== 'edit') {
        const headings = [...elements.markdownPreview.querySelectorAll('h1, h2, h3, h4, h5, h6')];
        const headingIndex = state.outline.indexOf(heading);
        const previewHeading = headings.find(element =>
            Number(element.dataset.sourceLine) === heading.bodyLine
        ) || headings[headingIndex] || headings.find(element =>
            element.textContent.trim() === heading.text
        );
        if (!previewHeading) return;
        requestAnimationFrame(() => {
            const preview = elements.markdownPreview;
            const targetTop = Math.max(0, Math.min(
                preview.scrollHeight - preview.clientHeight,
                preview.scrollTop + previewHeading.getBoundingClientRect().top - preview.getBoundingClientRect().top
            ));
            if (Math.abs(preview.scrollTop - targetTop) >= 1) {
                programmaticScroll = { element: preview, scrollTop: targetTop };
                preview.scrollTop = targetTop;
            }
        });
    }
}

function renderFolderPanel() {
    const scrollTop = elements.panel.scrollTop;
    const all = document.createElement('button');
    all.type = 'button';
    all.className = `tree-root${state.folder === '' ? ' is-selected' : ''}`;
    all.dataset.folder = '';
    all.textContent = 'Todas as notas';
    const root = document.createElement('section');
    root.className = 'tree-root-group';
    root.dataset.dropFolder = '';
    const rootHeader = document.createElement('div');
    rootHeader.className = 'tree-row';
    const rootExpand = makeExpandButton('', 'Notas sem pasta');
    const rootLabel = document.createElement('span');
    rootLabel.className = 'tree-group-label';
    rootLabel.textContent = 'Sem pasta';
    rootHeader.append(rootExpand, rootLabel);
    root.append(rootHeader);
    if (state.expandedFolders.has('')) root.append(renderDirectNotes('', true));
    const tree = document.createElement('ul');
    tree.className = 'folder-tree root-folder-tree';
    for (const folder of state.folders) tree.append(buildFolderNode(folder));
    elements.panelContent.replaceChildren(all, root, tree);
    elements.panel.scrollTop = scrollTop;
}

function makeExpandButton(folderPath, label) {
    const expand = document.createElement('button');
    expand.type = 'button';
    expand.className = 'tree-expand';
    expand.dataset.toggleFolder = folderPath;
    const expanded = state.expandedFolders.has(folderPath);
    expand.setAttribute('aria-label', `${expanded ? 'Recolher' : 'Expandir'} ${label}`);
    expand.setAttribute('aria-expanded', String(expanded));
    expand.textContent = expanded ? '⌄' : '›';
    return expand;
}

function buildFolderNode(folder) {
    const item = document.createElement('li');
    item.className = 'tree-node';
    const row = document.createElement('div');
    row.className = 'tree-row';
    row.append(makeExpandButton(folder.path, folder.name));
    const select = document.createElement('button');
    select.type = 'button';
    select.className = `tree-folder${state.folder === folder.path ? ' is-selected' : ''}`;
    select.dataset.folder = folder.path;
    select.dataset.dragFolder = folder.path;
    select.draggable = true;
    select.textContent = folder.name;
    row.dataset.dropFolder = folder.path;
    row.append(select);
    item.append(row);
    if (state.expandedFolders.has(folder.path)) {
        const contents = document.createElement('ul');
        contents.className = 'folder-tree';
        const directNotes = renderDirectNotes(folder.path, false);
        while (directNotes.firstChild) contents.append(directNotes.firstChild);
        for (const child of folder.children || []) contents.append(buildFolderNode(child));
        item.append(contents);
    }
    return item;
}

function renderDirectNotes(folder, root) {
    const list = document.createElement(root ? 'div' : 'ul');
    list.className = root ? 'tree-note-list root-note-list' : 'folder-tree-note-list';
    const page = state.folderNotes.get(folder);
    if (!page) {
        void loadFolderNotes(folder);
        const loading = document.createElement(root ? 'p' : 'li');
        loading.className = 'tree-loading';
        loading.textContent = 'Carregando…';
        list.append(loading);
        return list;
    }
    for (const note of page.items) {
        const item = document.createElement(root ? 'div' : 'li');
        item.className = 'tree-note-item';
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'tree-note';
        button.dataset.treeNote = String(note.id);
        button.dataset.dragNote = String(note.id);
        button.draggable = true;
        button.title = note.title;
        button.textContent = note.title;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.className = 'tree-note-delete';
        remove.dataset.deleteTreeNote = String(note.id);
        remove.setAttribute('aria-label', `Excluir ${note.title}`);
        remove.title = 'Excluir nota';
        remove.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M10 11v6m4-6v6M6 7l1 14h10l1-14M9 7V4h6v3"/></svg>';
        item.append(button, remove);
        list.append(item);
    }
    if (page.items.length < page.total) {
        const item = document.createElement(root ? 'div' : 'li');
        const more = document.createElement('button');
        more.type = 'button';
        more.className = 'tree-more';
        more.dataset.moreFolder = folder;
        more.textContent = 'Carregar mais notas';
        item.append(more);
        list.append(item);
    }
    if (!page.items.length) {
        const empty = document.createElement(root ? 'p' : 'li');
        empty.className = 'tree-loading';
        empty.textContent = 'Nenhuma nota nesta pasta';
        list.append(empty);
    }
    return list;
}

function handlePanelClick(event) {
    const outlineItem = event.target.closest('[data-outline-line]');
    if (outlineItem) {
        scrollToOutlineHeading(outlineItem.dataset.outlineLine);
        return;
    }
    const backlink = event.target.closest('[data-backlink-note]');
    if (backlink) {
        void openNote(Number(backlink.dataset.backlinkNote));
        return;
    }
    const remove = event.target.closest('[data-delete-tree-note]');
    if (remove) {
        event.preventDefault();
        event.stopPropagation();
        void deleteNote(Number(remove.dataset.deleteTreeNote));
        return;
    }
    const toggle = event.target.closest('[data-toggle-folder]');
    if (toggle) {
        event.preventDefault();
        event.stopPropagation();
        document.body.classList.add('panel-open');
        const folder = toggle.dataset.toggleFolder;
        if (state.expandedFolders.has(folder)) state.expandedFolders.delete(folder);
        else {
            state.expandedFolders.add(folder);
            void loadFolderNotes(folder);
        }
        renderFolderPanel();
        return;
    }
    const more = event.target.closest('[data-more-folder]');
    if (more) {
        void loadFolderNotes(more.dataset.moreFolder, true);
        return;
    }
    const folderButton = event.target.closest('[data-folder]');
    if (folderButton) {
        void selectFolder(folderButton.dataset.folder);
        return;
    }
    const note = event.target.closest('[data-tree-note]');
    if (note) {
        void openNote(Number(note.dataset.treeNote));
    }
}

function handleSidebarDragStart(event) {
    const note = event.target.closest('[data-drag-note]');
    const folder = event.target.closest('[data-drag-folder]');
    const source = note || folder;
    if (!source || !event.dataTransfer) return;
    const type = note ? 'note' : 'folder';
    const value = note ? note.dataset.dragNote : folder.dataset.dragFolder;
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData(`application/x-pkm-${type}`, value);
    source.classList.add('is-dragging');
}

function handleSidebarDragOver(event) {
    const target = event.target.closest('[data-drop-folder]');
    if (!target || !event.dataTransfer) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    target.classList.add('is-drop-target');
}

function handleSidebarDragLeave(event) {
    const target = event.target.closest('[data-drop-folder]');
    if (target && !target.contains(event.relatedTarget)) target.classList.remove('is-drop-target');
}

function clearDropTargets() {
    elements.panelContent.querySelectorAll('.is-drop-target, .is-dragging').forEach(element => {
        element.classList.remove('is-drop-target', 'is-dragging');
    });
}

async function handleSidebarDrop(event) {
    const target = event.target.closest('[data-drop-folder]');
    if (!target || !event.dataTransfer) return;
    event.preventDefault();
    const destinationParent = target.dataset.dropFolder;
    const noteID = event.dataTransfer.getData('application/x-pkm-note');
    const sourceFolder = event.dataTransfer.getData('application/x-pkm-folder');
    clearDropTargets();
    if (noteID) {
        await moveNoteToFolder(Number(noteID), destinationParent);
    } else if (sourceFolder) {
        await moveFolderToParent(sourceFolder, destinationParent);
    }
}

async function moveNoteToFolder(noteID, folder) {
    try {
        if (state.view === 'editor' && state.noteID === noteID && !(await flushSave())) return;
        await requestJSON(`/api/notes/${noteID}/folder`, {
            method: 'PATCH',
            body: JSON.stringify({ folder })
        });
        state.graphData = null;
        if (state.view === 'editor' && state.noteID === noteID) {
            state.noteFolder = folder;
            state.savedDraft = getDraft();
            updateEditorBreadcrumb();
        }
        showMessage('Nota movida.', 'success');
        await refreshFolderTree();
        await loadNotes();
    } catch (error) {
        showMessage(`Não foi possível mover a nota: ${error.message}`, 'error');
    }
}

async function moveFolderToParent(source, target) {
    try {
        if (state.view === 'editor' && state.noteFolder &&
            (state.noteFolder === source || state.noteFolder.startsWith(`${source}/`)) &&
            !(await flushSave())) return;
        const result = await requestJSON('/api/folders/move', {
            method: 'POST',
            body: JSON.stringify({ source, target })
        });
        state.graphData = null;
        state.folder = rebaseFolderPath(state.folder, source, result.folder);
        if (state.listState) {
            state.listState.folder = rebaseFolderPath(state.listState.folder || '', source, result.folder);
        }
        if (state.view === 'editor' && (state.noteFolder === source || state.noteFolder.startsWith(`${source}/`))) {
            state.noteFolder = rebaseFolderPath(state.noteFolder, source, result.folder);
            state.savedDraft = getDraft();
            updateEditorBreadcrumb();
            const current = history.state || {};
            history.replaceState({ ...current, list: state.listState }, '', location.href);
        }
        showMessage('Pasta movida.', 'success');
        updateListHistory();
        await refreshFolderTree();
        await loadNotes();
    } catch (error) {
        showMessage(`Não foi possível mover a pasta: ${error.message}`, 'error');
    }
}

function rebaseFolderPath(folder, source, destination) {
    if (folder === source) return destination;
    return folder.startsWith(`${source}/`) ? destination + folder.slice(source.length) : folder;
}

async function refreshFolderTree() {
    state.folderNotesGeneration++;
    state.folderNotes.clear();
    await loadFolders();
    const folders = ['', ...state.expandedFolders].filter((folder, index, list) => list.indexOf(folder) === index);
    await Promise.all(folders.map(folder => loadFolderNotes(folder)));
}

async function selectFolder(folder) {
    if (state.view === 'editor' && !(await flushSave())) return;
    if (state.graphVisible) closeGraphView();
    if (state.view === 'editor') {
        history.pushState({ view: 'list', list: { ...captureListState(), folder, offset: 0 } }, '', location.pathname);
        state.view = 'list';
        state.noteID = null;
        resetBacklinks();
        resetOutline();
        document.body.classList.remove('is-editor-view');
        elements.editorView.hidden = true;
        elements.searchView.hidden = false;
        elements.newNote.hidden = false;
    }
    state.folder = folder;
    state.offset = 0;
    if (window.matchMedia('(max-width: 700px)').matches) closePanel();
    loadNotes();
    updateListHistory();
    if (elements.panel.dataset.panel === 'files') renderFolderPanel();
}

function setupTheme() {
    const saved = localStorage.getItem('theme');
    document.documentElement.dataset.theme = saved === 'light' ? 'light' : 'dark';
}

function toggleTheme() {
    const next = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    localStorage.setItem('theme', next);
}

async function requestJSON(url, options = {}) {
    const response = await fetch(url, {
        ...options,
        headers: {
            ...(options.body ? { 'Content-Type': 'application/json' } : {}),
            ...options.headers
        }
    });
    if (!response.ok) {
        const message = (await response.text()).trim();
        if (response.status === 401) location.replace('/login.html');
        throw new Error(message || `HTTP ${response.status}`);
    }
    if (response.status === 204) return null;
    return response.json();
}

function showMessage(message, kind = '') {
    window.clearTimeout(showMessage.clearTimer);
    elements.appMessage.textContent = message;
    elements.appMessage.className = `app-message${kind ? ` is-${kind}` : ''}`;
    if (!message) return;
    showMessage.clearTimer = window.setTimeout(() => {
        if (elements.appMessage.textContent === message) {
            elements.appMessage.textContent = '';
            elements.appMessage.className = 'app-message';
        }
    }, kind === 'success' ? 2800 : 6000);
}
