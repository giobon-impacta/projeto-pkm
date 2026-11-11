package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

const (
	defaultPageSize = 30
	maxPageSize     = 100
	maxContentSize  = 5 << 20
	maxRequestSize  = maxContentSize + (64 << 10)
)

type Note struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Folder    string   `json:"folder"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type NoteSummary struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Folder    string   `json:"folder"`
	Tags      []string `json:"tags,omitempty"`
	Preview   string   `json:"preview"`
	UpdatedAt string   `json:"updated_at"`
}

type Page struct {
	Items  []NoteSummary `json:"items"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

type Folder struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Children []Folder `json:"children"`
}

type App struct {
	db *sql.DB
}

type folderBuilder struct {
	Folder
	children map[string]*folderBuilder
}

func NewApp(dataDir string) (*App, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "notes.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	app := &App{db: db}

	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure database (%s): %w", statement, err)
		}
	}
	if err := app.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	log.Printf("Database initialized at %s", filepath.Join(dataDir, "notes.db"))
	return app, nil
}

func (a *App) Close() error {
	return a.db.Close()
}

func (a *App) migrate() error {
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS notes (
		rowid INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		content TEXT DEFAULT '',
		folder TEXT DEFAULT '',
		tags TEXT DEFAULT '',
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}
	if _, err := a.db.Exec(`CREATE INDEX IF NOT EXISTS idx_notes_folder ON notes(folder)`); err != nil {
		return err
	}
	if _, err := a.db.Exec(`CREATE INDEX IF NOT EXISTS idx_notes_folder_title ON notes(folder, title COLLATE NOCASE)`); err != nil {
		return err
	}
	if _, err := a.db.Exec(`CREATE INDEX IF NOT EXISTS idx_notes_updated ON notes(updated_at DESC)`); err != nil {
		return err
	}
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}

	var applied int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}

	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
			title,
			content,
			content='notes',
			content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		)`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_insert AFTER INSERT ON notes BEGIN
			INSERT INTO notes_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_delete AFTER DELETE ON notes BEGIN
			INSERT INTO notes_fts(notes_fts, rowid, title, content)
			VALUES ('delete', old.rowid, old.title, old.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_update AFTER UPDATE ON notes BEGIN
			INSERT INTO notes_fts(notes_fts, rowid, title, content)
			VALUES ('delete', old.rowid, old.title, old.content);
			INSERT INTO notes_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
		END`,
		`INSERT INTO notes_fts(notes_fts) VALUES ('rebuild')`,
		`INSERT INTO schema_migrations(version) VALUES (1)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) ListNotes(folder string, limit, offset int) (Page, error) {
	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM notes WHERE folder = ? OR ? = ''", folder, folder).Scan(&total); err != nil {
		return Page{}, err
	}
	rows, err := a.db.Query(`SELECT rowid, title, folder, substr(COALESCE(content, ''), 1, 180), updated_at, COALESCE(tags, '')
		FROM notes WHERE folder = ? OR ? = ''
		ORDER BY updated_at DESC, rowid DESC LIMIT ? OFFSET ?`, folder, folder, limit, offset)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	items, err := scanSummaries(rows)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (a *App) ListFolderNotes(folder string, limit, offset int) (Page, error) {
	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM notes WHERE folder = ?", folder).Scan(&total); err != nil {
		return Page{}, err
	}
	rows, err := a.db.Query(`SELECT rowid, title, folder, substr(COALESCE(content, ''), 1, 180), updated_at, COALESCE(tags, '')
		FROM notes WHERE folder = ?
		ORDER BY title COLLATE NOCASE, rowid LIMIT ? OFFSET ?`, folder, limit, offset)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	items, err := scanSummaries(rows)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (a *App) SearchNotes(query, mode, folder string, limit, offset int) (Page, error) {
	match, err := makeFTSQuery(query, mode)
	if err != nil {
		return Page{}, err
	}
	preview := "substr(COALESCE(n.content, ''), 1, 180)"
	if mode == "content" {
		preview = "snippet(notes_fts, 1, '<mark>', '</mark>', '…', 24)"
	}
	var total int
	if err := a.db.QueryRow(`SELECT COUNT(*)
		FROM notes_fts JOIN notes n ON n.rowid = notes_fts.rowid
		WHERE notes_fts MATCH ? AND (n.folder = ? OR ? = '')`,
		match, folder, folder).Scan(&total); err != nil {
		return Page{}, err
	}
	rows, err := a.db.Query(`SELECT n.rowid, n.title, n.folder, `+preview+`, n.updated_at, COALESCE(n.tags, '')
		FROM notes_fts JOIN notes n ON n.rowid = notes_fts.rowid
		WHERE notes_fts MATCH ? AND (n.folder = ? OR ? = '')
		ORDER BY notes_fts.rank, n.updated_at DESC, n.rowid DESC LIMIT ? OFFSET ?`,
		match, folder, folder, limit, offset)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	items, err := scanSummaries(rows)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func scanSummaries(rows *sql.Rows) ([]NoteSummary, error) {
	items := make([]NoteSummary, 0)
	for rows.Next() {
		var item NoteSummary
		var tags string
		if err := rows.Scan(&item.ID, &item.Title, &item.Folder, &item.Preview, &item.UpdatedAt, &tags); err != nil {
			return nil, err
		}
		if tags != "" {
			for _, tag := range strings.Split(tags, ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					item.Tags = append(item.Tags, tag)
				}
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func makeFTSQuery(query, mode string) (string, error) {
	switch mode {
	case "title", "content":
	default:
		return "", errors.New("mode must be title or content")
	}
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 256 {
		return "", errors.New("query must contain between 1 and 256 characters")
	}
	terms := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(terms) == 0 {
		return "", errors.New("query must contain searchable characters")
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"*`)
	}
	return mode + " : (" + strings.Join(quoted, " AND ") + ")", nil
}

func (a *App) GetNote(id int64) (Note, error) {
	var note Note
	var tags string
	err := a.db.QueryRow(`SELECT rowid, title, COALESCE(content, ''), COALESCE(folder, ''),
		COALESCE(tags, ''), created_at, updated_at FROM notes WHERE rowid = ?`, id).
		Scan(&note.ID, &note.Title, &note.Content, &note.Folder, &tags, &note.CreatedAt, &note.UpdatedAt)
	if err != nil {
		return Note{}, err
	}
	if tags != "" {
		for _, tag := range strings.Split(tags, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				note.Tags = append(note.Tags, tag)
			}
		}
	}
	return note, nil
}

func (a *App) CreateNote(note Note) (int64, error) {
	if err := validateNote(&note); err != nil {
		return 0, err
	}
	result, err := a.db.Exec("INSERT INTO notes(title, content, folder, tags) VALUES (?, ?, ?, ?)",
		note.Title, note.Content, note.Folder, strings.Join(note.Tags, ","))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (a *App) UpdateNote(id int64, note Note) error {
	if err := validateNote(&note); err != nil {
		return err
	}
	result, err := a.db.Exec(`UPDATE notes SET title = ?, content = ?, folder = ?, tags = ?,
		updated_at = datetime('now') WHERE rowid = ?`,
		note.Title, note.Content, note.Folder, strings.Join(note.Tags, ","), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (a *App) MoveNoteFolder(id int64, folder string) error {
	normalized, err := normalizeFolder(folder)
	if err != nil || normalized != folder {
		return errors.New("folder must be a normalized relative path")
	}
	result, err := a.db.Exec(`UPDATE notes SET folder = ?, updated_at = datetime('now') WHERE rowid = ?`, folder, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (a *App) MoveFolder(source, target string) (string, int64, error) {
	normalizedSource, err := normalizeFolder(source)
	if err != nil || normalizedSource != source || source == "" {
		return "", 0, errors.New("source must be a non-empty normalized folder path")
	}
	normalizedTarget, err := normalizeFolder(target)
	if err != nil || normalizedTarget != target {
		return "", 0, errors.New("target must be a normalized relative folder path")
	}
	if target == source || strings.HasPrefix(target, source+"/") {
		return "", 0, errors.New("a folder cannot be moved into itself or one of its descendants")
	}

	name := path.Base(source)
	destination := name
	if target != "" {
		destination = path.Join(target, name)
	}
	if len(destination) > 1024 {
		return "", 0, errors.New("destination folder path exceeds the 1024 character limit")
	}
	if destination == source {
		return "", 0, errors.New("folder is already in that location")
	}

	tx, err := a.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	var count int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM notes
		WHERE folder = ? OR substr(folder, 1, length(?) + 1) = ? || '/'`,
		source, source, source).Scan(&count); err != nil {
		return "", 0, err
	}
	if count == 0 {
		return "", 0, sql.ErrNoRows
	}
	var maxPathLength int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(length(CAST(? || substr(folder, length(?) + 1) AS BLOB))), 0)
		FROM notes WHERE folder = ? OR substr(folder, 1, length(?) + 1) = ? || '/'`,
		destination, source, source, source, source).Scan(&maxPathLength); err != nil {
		return "", 0, err
	}
	if maxPathLength > 1024 {
		return "", 0, errors.New("destination would make a folder path exceed the 1024 byte limit")
	}
	if _, err := tx.Exec(`UPDATE notes SET folder = ? || substr(folder, length(?) + 1),
		updated_at = datetime('now')
		WHERE folder = ? OR substr(folder, 1, length(?) + 1) = ? || '/'`,
		destination, source, source, source, source); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	return destination, count, nil
}

func (a *App) DeleteNote(id int64) error {
	result, err := a.db.Exec("DELETE FROM notes WHERE rowid = ?", id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func validateNote(note *Note) error {
	note.Title = strings.TrimSpace(note.Title)
	if note.Title == "" || len([]rune(note.Title)) > 256 {
		return errors.New("title must contain between 1 and 256 characters")
	}
	if len(note.Content) > maxContentSize {
		return errors.New("content exceeds the 5 MiB limit")
	}
	folder, err := normalizeFolder(note.Folder)
	if err != nil {
		return err
	}
	note.Folder = folder
	if tags, found := frontmatterTags(note.Content); found {
		note.Tags = tags
	}
	for i := range note.Tags {
		note.Tags[i] = strings.TrimSpace(note.Tags[i])
		if strings.Contains(note.Tags[i], ",") {
			return errors.New("tags cannot contain commas")
		}
	}
	return nil
}

func frontmatterTags(content string) ([]string, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return nil, false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if marker := strings.TrimSpace(lines[i]); marker == "---" || marker == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, false
	}
	for i := 1; i < end; i++ {
		key, value, found := strings.Cut(strings.TrimSpace(lines[i]), ":")
		if !found || strings.TrimSpace(strings.TrimSuffix(key, " ")) != "tags" {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			value = strings.TrimSpace(value[1 : len(value)-1])
		}
		tags := make([]string, 0)
		if value != "" {
			for _, tag := range strings.Split(value, ",") {
				tag = strings.TrimSpace(strings.Trim(tag, `"'`))
				if tag != "" {
					tags = append(tags, tag)
				}
			}
		} else {
			for j := i + 1; j < end; j++ {
				item := strings.TrimSpace(lines[j])
				if !strings.HasPrefix(item, "-") {
					if strings.Contains(item, ":") {
						break
					}
					continue
				}
				tag := strings.TrimSpace(strings.Trim(strings.TrimSpace(strings.TrimPrefix(item, "-")), `"'`))
				if tag != "" {
					tags = append(tags, tag)
				}
			}
		}
		return tags, true
	}
	return nil, false
}

func normalizeFolder(value string) (string, error) {
	if len(value) > 1024 {
		return "", errors.New("folder path exceeds the 1024 character limit")
	}
	folder := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if strings.HasPrefix(folder, "/") {
		return "", errors.New("folder path must be relative")
	}
	if folder != "" {
		for _, component := range strings.Split(folder, "/") {
			if component == ".." {
				return "", errors.New("folder path cannot contain '..'")
			}
		}
		folder = path.Clean(folder)
		if folder == "." {
			folder = ""
		}
	}
	return folder, nil
}

func (a *App) FolderTree() ([]Folder, error) {
	rows, err := a.db.Query("SELECT DISTINCT folder FROM notes WHERE folder != '' ORDER BY folder")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	root := &folderBuilder{children: make(map[string]*folderBuilder)}
	for rows.Next() {
		var folder string
		if err := rows.Scan(&folder); err != nil {
			return nil, err
		}
		current := root
		parts := strings.Split(strings.Trim(folder, "/"), "/")
		currentPath := ""
		for _, name := range parts {
			if name == "" {
				continue
			}
			if currentPath == "" {
				currentPath = name
			} else {
				currentPath += "/" + name
			}
			child := current.children[name]
			if child == nil {
				child = &folderBuilder{Folder: Folder{Name: name, Path: currentPath}, children: make(map[string]*folderBuilder)}
				current.children[name] = child
			}
			current = child
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return folderValues(root), nil
}

func folderValues(parent *folderBuilder) []Folder {
	names := make([]string, 0, len(parent.children))
	for name := range parent.children {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]Folder, 0, len(names))
	for _, name := range names {
		child := parent.children[name]
		child.Children = folderValues(child)
		result = append(result, child.Folder)
	}
	return result
}

func (a *App) Handler(frontendDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", a.handleHealth)
	mux.HandleFunc("/api/notes", a.handleNotes)
	mux.HandleFunc("/api/notes/", a.handleNote)
	mux.HandleFunc("/api/search", a.handleSearch)
	mux.HandleFunc("/api/folders", a.handleFolders)
	mux.HandleFunc("/api/folders/move", a.handleFolderMove)
	mux.HandleFunc("/api/folder-notes", a.handleFolderNotes)
	mux.HandleFunc("/api/preview", handlePreview)
	mux.Handle("/", http.FileServer(http.Dir(frontendDir)))
	return mux
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleNotes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, offset, err := pageParameters(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		page, err := a.ListNotes(r.URL.Query().Get("folder"), limit, offset)
		if err != nil {
			log.Printf("list notes: %v", err)
			http.Error(w, "failed to list notes", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, page)
	case http.MethodPost:
		var note Note
		if err := decodeNote(w, r, &note); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateNote(&note); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id, err := a.CreateNote(note)
		if err != nil {
			log.Printf("create note: %v", err)
			http.Error(w, "failed to create note", http.StatusInternalServerError)
			return
		}
		created, err := a.GetNote(id)
		if err != nil {
			log.Printf("read created note %d: %v", id, err)
			http.Error(w, "note created but could not be loaded", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		methodNotAllowed(w)
	}
}

func (a *App) handleNote(w http.ResponseWriter, r *http.Request) {
	rawID := strings.TrimPrefix(r.URL.Path, "/api/notes/")
	parts := strings.Split(rawID, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 || len(parts) > 2 || (len(parts) == 2 && parts[1] != "folder") {
		http.Error(w, "invalid note ID", http.StatusBadRequest)
		return
	}
	if len(parts) == 2 {
		a.handleNoteFolderMove(w, r, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		note, err := a.GetNote(id)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			log.Printf("get note %d: %v", id, err)
			http.Error(w, "failed to load note", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, note)
	case http.MethodPut:
		var note Note
		if err := decodeNote(w, r, &note); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateNote(&note); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := a.UpdateNote(id, note); errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else if err != nil {
			log.Printf("update note %d: %v", id, err)
			http.Error(w, "failed to update note", http.StatusInternalServerError)
		} else {
			updated, err := a.GetNote(id)
			if err != nil {
				log.Printf("read updated note %d: %v", id, err)
				http.Error(w, "note saved but could not be loaded", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, updated)
		}
	case http.MethodDelete:
		if err := a.DeleteNote(id); errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else if err != nil {
			log.Printf("delete note %d: %v", id, err)
			http.Error(w, "failed to delete note", http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		methodNotAllowed(w)
	}
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	limit, offset, err := pageParameters(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := a.SearchNotes(r.URL.Query().Get("q"), r.URL.Query().Get("mode"),
		r.URL.Query().Get("folder"), limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *App) handleFolders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	folders, err := a.FolderTree()
	if err != nil {
		log.Printf("load folder tree: %v", err)
		http.Error(w, "failed to load folders", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, folders)
}

func (a *App) handleFolderMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var request struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	destination, count, err := a.MoveFolder(request.Source, request.Target)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folder": destination, "moved": count})
}

func (a *App) handleNoteFolderMove(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPatch {
		methodNotAllowed(w)
		return
	}
	var request struct {
		Folder string `json:"folder"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.MoveNoteFolder(id, request.Folder); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	} else {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "folder": request.Folder})
	}
}

func (a *App) handleFolderNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	limit, offset, err := pageParameters(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	folder := r.URL.Query().Get("folder")
	normalized, err := normalizeFolder(folder)
	if err != nil || normalized != folder {
		http.Error(w, "folder must be a normalized relative path", http.StatusBadRequest)
		return
	}
	page, err := a.ListFolderNotes(folder, limit, offset)
	if err != nil {
		log.Printf("list notes in folder %q: %v", folder, err)
		http.Error(w, "failed to list folder notes", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	source, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxContentSize))
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			http.Error(w, "markdown content exceeds the 5 MiB limit", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "failed to read markdown content", http.StatusBadRequest)
		}
		return
	}

	md := goldmark.New()
	document := md.Parser().Parse(text.NewReader(source))
	if err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch link := node.(type) {
		case *ast.Link:
			if !safeMarkdownDestination(link.Destination) {
				link.Destination = []byte("#")
			}
		case *ast.Image:
			if !safeMarkdownDestination(link.Destination) {
				link.Destination = []byte("#")
			}
		}
		return ast.WalkContinue, nil
	}); err != nil {
		log.Printf("walk markdown AST: %v", err)
		http.Error(w, "failed to render markdown", http.StatusInternalServerError)
		return
	}

	var rendered bytes.Buffer
	if err := md.Renderer().Render(&rendered, source, document); err != nil {
		log.Printf("render markdown: %v", err)
		http.Error(w, "failed to render markdown", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(rendered.Bytes()); err != nil {
		log.Printf("write markdown preview: %v", err)
	}
}

func safeMarkdownDestination(destination []byte) bool {
	parsed, err := url.Parse(strings.TrimSpace(string(destination)))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "", "http", "https", "mailto":
		return true
	default:
		return false
	}
}

func pageParameters(r *http.Request) (int, int, error) {
	limit := defaultPageSize
	offset := 0
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxPageSize {
			return 0, 0, fmt.Errorf("limit must be between 1 and %d", maxPageSize)
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
	}
	return limit, offset, nil
}

func decodeNote(w http.ResponseWriter, r *http.Request, note *Note) error {
	return decodeJSON(w, r, note)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestSize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func main() {
	defaultDataDir := os.Getenv("NOTES_DIR")
	if defaultDataDir == "" {
		defaultDataDir = "./data"
	}
	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8000"
	}
	dataDir := flag.String("data-dir", defaultDataDir, "Directory for SQLite note data")
	port := flag.String("port", defaultPort, "HTTP port")
	flag.Parse()
	portNumber, err := strconv.Atoi(*port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		log.Fatalf("invalid port %q: expected 1-65535", *port)
	}

	app, err := NewApp(*dataDir)
	if err != nil {
		log.Fatalf("initialize application: %v", err)
	}
	defer app.Close()

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", portNumber),
		Handler:           app.Handler("./web/frontend"),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("PKM Notes listening on http://localhost:%d", portNumber)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve application: %v", err)
	}
}
