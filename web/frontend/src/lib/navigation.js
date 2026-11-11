// Navigation module - history, popstate, folder navigation, and panel management
import { requestJSON } from './api-client.js';

export const navigationModule = {
    // Initialize navigation functionality
    init() {
        this.attachPopState();
        this.attachPanelButtons();
        this.attachPanelResize();
        this.attachThemeToggle();
        this.attachHomeButton();
    },

    // Attach popstate handler
    attachPopState() {
        window.addEventListener('popstate', event => this.handlePopState(event));
    },

    // Handle popstate events
    handlePopState(event) {
        const target = event.state || { view: 'list', list: state.listState };
        if (state.view === 'editor' && !this.flushSave()) {
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
        document.querySelector('#editorView')?.hidden = true;
        document.querySelector('#graphView')?.hidden = !state.graphVisible;
        document.querySelector('#newNote')?.hidden = false;
        loadNotes();
        if (document.querySelector('#panel').dataset.panel === 'search') document.querySelector('#searchInput').focus();
    },

    // Attach panel toggle buttons
    attachPanelButtons() {
        // Home button
        const homeButton = document.querySelector('#homeButton');
        if (homeButton) {
            homeButton.addEventListener('click', () => this.showAllNotes());
        }
    },

    // Show all notes
    showAllNotes() {
        const allNotes = { query: '', mode: 'title', folder: '', tag: '', graphVisible: false };
        if (state.view === 'editor') {
            this.returnToList(allNotes, true);
            return;
        }
        if (state.graphVisible) this.closeGraphView();
        this.restoreListState(allNotes);
        state.view = 'list';
        state.noteID = null;
        document.querySelector('#editorView')?.hidden = true;
        document.querySelector('#newNote')?.hidden = false;
        document.body.classList.remove('is-editor-view');
        this.updateListHistory();
        loadNotes();
        document.querySelector('#searchInput').focus();
    },

    // Return to list view (from editor)
    returnToList(listState, replace = false) {
        if (!this.flushSave()) return;
        const target = listState || { query: '', mode: 'title', folder: '', offset: 0 };
        if (replace) history.replaceState({ view: 'list', list: target }, '', location.pathname);
        state.view = 'list';
        state.noteID = null;
        resetBacklinks();
        resetOutline();
        document.body.classList.remove('is-editor-view');
        state.directEntry = false;
        restoreListState(target);
        document.querySelector('#editorView')?.hidden = true;
        document.querySelector('#graphView')?.hidden = !state.graphVisible;
        document.querySelector('#newNote')?.hidden = false;
        loadNotes();
        if (document.querySelector('#panel').dataset.panel === 'search') document.querySelector('#searchInput').focus();
    },

    // Close graph view
    closeGraphView() {
        if (!state.graphVisible) return;
        state.graphVisible = false;
        state.graphRequestID++;
        document.querySelector('#graphView')?.hidden = true;
        document.querySelector('#searchView')?.hidden = false;
        const graphButton = document.querySelector('#graphButton');
        if (graphButton) {
            graphButton.classList.remove('is-active');
            graphButton.setAttribute('aria-pressed', 'false');
        }
        this.updateListHistory();
    },

    // Attach workspace panel resize
    attachPanelResize() {
        const resizer = document.querySelector('#workspacePanelResizer');
        if (!resizer) return;

        let isResizing = false;

        const startResize = event => {
            if (event.button !== 0 || window.matchMedia('(min-width: 701px)').matches) return;
            event.preventDefault();
            resizer.setPointerCapture(event.pointerId);
            document.body.classList.add('is-resizing-editor');
            this.resizeSplit(event);
        };

        const resize = event => {
            if (!resizer.hasPointerCapture?.(event.pointerId)) return;
            const bounds = document.querySelector('#workspacePanel')?.getBoundingClientRect();
            if (!bounds) return;
            editorSplitPercent = Math.min(80, Math.max(20, ((event.clientX - bounds.left) / bounds.width) * 100));
            this.updateSplitRatio();
            this.syncPreviewMinHeight();
            this.syncEditorScrollFromSource();
        };

        const stopResize = event => {
            if (!resizer.hasPointerCapture?.(event.pointerId)) return;
            resizer.releasePointerCapture(event.pointerId);
            document.body.classList.remove('is-resizing-editor');
            localStorage.setItem('editorSplitPercent', String(Math.round(editorSplitPercent)));
        };

        const resizeKey = event => {
            if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
            event.preventDefault();
            if (event.key === 'Home') editorSplitPercent = 20;
            else if (event.key === 'End') editorSplitPercent = 80;
            else editorSplitPercent = Math.min(80, Math.max(20, editorSplitPercent + (event.key === 'ArrowRight' ? 5 : -5)));
            this.updateSplitRatio();
            localStorage.setItem('editorSplitPercent', String(Math.round(editorSplitPercent)));
        };

        resizer.addEventListener('pointerdown', startResize);
        window.addEventListener('pointermove', resize);
        window.addEventListener('pointerup', stopResize);
        window.addEventListener('pointercancel', stopResize);
        window.addEventListener('keydown', resizeKey);
    },

    updateSplitRatio() {
        const splitter = document.querySelector('.editor-splitter');
        if (!splitter) return;
        document.querySelector('#editorWorkspace')?.style.setProperty('--editor-left', `${editorSplitPercent}fr`);
        document.querySelector('#editorWorkspace')?.style.setProperty('--editor-right', `${100 - editorSplitPercent}fr`);
        splitter.setAttribute('aria-valuenow', String(Math.round(editorSplitPercent)));
    },

    // Attach theme toggle
    attachThemeToggle() {
        const themeToggle = document.querySelector('#themeToggle');
        if (!themeToggle) return;
        themeToggle.addEventListener('click', () => this.toggleTheme());
    },

    toggleTheme() {
        document.documentElement.dataset.theme = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
        localStorage.setItem('theme', document.documentElement.dataset.theme);
    },

    // Attach home button
    attachHomeButton() {
        const homeButton = document.querySelector('#homeButton');
        if (homeButton) {
            homeButton.addEventListener('click', () => showAllNotes());
        }
    },

    // Handle folder panel rendering (simplified)
    renderFolderPanel() {
        // This would contain the full folder panel logic
        // For now, just a placeholder
        const panelContent = document.querySelector('#panelContent');
        if (!panelContent) return;
        panelContent.replaceChildren();
        const all = document.createElement('button');
        all.type = 'button';
        all.className = `tree-root${state.folder === '' ? ' is-selected' : ''}`;
        all.dataset.folder = '';
        all.textContent = 'Todas as notas';
        panelContent.append(all);
    },

    // Show graph view
    openGraphView() {
        if (state.view === 'editor') {
            if (!(this.flushSave())) {
                this.showMessage('Salve a nota antes de abrir o grafo.', 'error');
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
            document.querySelector('#editorView')?.hidden = true;
            document.querySelector('#newNote')?.hidden = false;
            this.restoreListState({ ...listState, graphVisible: false });
            loadNotes();
        }
        if (state.view !== 'list') return;
        state.graphVisible = true;
        document.querySelector('#searchView')?.hidden = true;
        document.querySelector('#graphView')?.hidden = false;
        const graphButton = document.querySelector('#graphButton');
        if (graphButton) {
            graphButton.classList.add('is-active');
            graphButton.setAttribute('aria-pressed', 'true');
        }
        this.updateListHistory();
        // graphView.resize() would be called here
    },

    // Full screen all notes
    showAllNotes() {
        const allNotes = { query: '', mode: 'title', folder: '', tag: '', graphVisible: false };
        if (state.view === 'editor') {
            this.returnToList(allNotes, true);
            return;
        }
        if (state.graphVisible) this.closeGraphView();
        this.restoreListState(allNotes);
        state.view = 'list';
        state.noteID = null;
        document.querySelector('#editorView')?.hidden = true;
        document.querySelector('#newNote')?.hidden = false;
        document.body.classList.remove('is-editor-view');
        this.updateListHistory();
        loadNotes();
        document.querySelector('#searchInput').focus();
    }
};