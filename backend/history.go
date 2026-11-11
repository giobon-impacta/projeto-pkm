package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

const noteVersionLimit = 50

// migrateNoteVersions stores snapshots of notes before they are modified.
// Keeping the last few versions per note lets the editor undo an accidental
// overwrite without shipping a full database backup.
func migrateNoteHistory(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS note_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id INTEGER NOT NULL REFERENCES notes(rowid) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			title TEXT NOT NULL,
			content TEXT DEFAULT '',
			folder TEXT DEFAULT '',
			tags TEXT DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(note_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_note_versions_note ON note_versions(note_id, id DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// snapshotNoteVersion copies the current state of a note into note_versions
// and trims the oldest snapshots. Callers run it inside the transaction that
// is about to change the note, so a failed update leaves no stray snapshot.
func snapshotNoteVersion(tx *sql.Tx, noteID int64) error {
	if _, err := tx.Exec(`INSERT OR IGNORE INTO note_versions(note_id, version, title, content, folder, tags)
		SELECT rowid, version, title, COALESCE(content, ''), COALESCE(folder, ''), COALESCE(tags, '')
		FROM notes WHERE rowid = ?`, noteID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM note_versions WHERE note_id = ? AND id NOT IN (
		SELECT id FROM note_versions WHERE note_id = ? ORDER BY id DESC LIMIT ?)`,
		noteID, noteID, noteVersionLimit)
	return err
}

type NoteVersion struct {
	ID        int64    `json:"id"`
	NoteID    int64    `json:"note_id"`
	Version   int64    `json:"version"`
	Title     string   `json:"title"`
	Folder    string   `json:"folder"`
	Tags      []string `json:"tags,omitempty"`
	Preview   string   `json:"preview"`
	Size      int64    `json:"size"`
	CreatedAt string   `json:"created_at"`
	Content   string   `json:"content,omitempty"`
}

func (a *App) ListNoteVersions(noteID int64) ([]NoteVersion, error) {
	var exists int
	if err := a.db.QueryRow("SELECT 1 FROM notes WHERE rowid = ?", noteID).Scan(&exists); err != nil {
		return nil, err
	}
	rows, err := a.db.Query(`SELECT id, note_id, version, title, COALESCE(folder, ''),
		COALESCE(tags, ''), substr(COALESCE(content, ''), 1, 180),
		length(COALESCE(content, '')), created_at
		FROM note_versions WHERE note_id = ? ORDER BY id DESC`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make([]NoteVersion, 0)
	for rows.Next() {
		var item NoteVersion
		var tags string
		if err := rows.Scan(&item.ID, &item.NoteID, &item.Version, &item.Title, &item.Folder,
			&tags, &item.Preview, &item.Size, &item.CreatedAt); err != nil {
			return nil, err
		}
		for _, tag := range strings.Split(tags, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				item.Tags = append(item.Tags, tag)
			}
		}
		versions = append(versions, item)
	}
	return versions, rows.Err()
}

func (a *App) GetNoteVersion(noteID, versionID int64) (NoteVersion, error) {
	var item NoteVersion
	var tags string
	err := a.db.QueryRow(`SELECT id, note_id, version, title, COALESCE(content, ''),
		COALESCE(folder, ''), COALESCE(tags, ''), created_at
		FROM note_versions WHERE id = ? AND note_id = ?`, versionID, noteID).
		Scan(&item.ID, &item.NoteID, &item.Version, &item.Title, &item.Content,
			&item.Folder, &tags, &item.CreatedAt)
	if err != nil {
		return NoteVersion{}, err
	}
	item.Preview = item.Content
	if len([]rune(item.Preview)) > 180 {
		item.Preview = string([]rune(item.Preview)[:180])
	}
	item.Size = int64(len(item.Content))
	for _, tag := range strings.Split(tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			item.Tags = append(item.Tags, tag)
		}
	}
	return item, nil
}

// RestoreNoteVersion makes the snapshot the current note again. The snapshot
// of the current state is recorded first, so a restore can also be undone.
func (a *App) RestoreNoteVersion(noteID, versionID, expectedVersion int64) (Note, error) {
	version, err := a.GetNoteVersion(noteID, versionID)
	if err != nil {
		return Note{}, err
	}
	restored := Note{
		Title:   version.Title,
		Content: version.Content,
		Folder:  version.Folder,
		Tags:    version.Tags,
		Version: expectedVersion,
	}
	if err := a.UpdateNote(noteID, restored); err != nil {
		return Note{}, err
	}
	return a.GetNote(noteID)
}

func (a *App) handleNoteVersions(w http.ResponseWriter, r *http.Request, noteID int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	versions, err := a.ListNoteVersions(noteID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("list note versions %d: %v", noteID, err)
		http.Error(w, "failed to load note history", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func (a *App) handleNoteVersion(w http.ResponseWriter, r *http.Request, noteID, versionID int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	version, err := a.GetNoteVersion(noteID, versionID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("load note version %d/%d: %v", noteID, versionID, err)
		http.Error(w, "failed to load note version", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (a *App) handleNoteVersionRestore(w http.ResponseWriter, r *http.Request, noteID, versionID int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var payload struct {
		Version int64 `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&payload); err != nil &&
		!errors.Is(err, io.EOF) {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	note, err := a.RestoreNoteVersion(noteID, versionID, payload.Version)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, ErrVersionConflict) {
		current, getErr := a.GetNote(noteID)
		if getErr != nil {
			http.Error(w, "note was modified by another session", http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "note was modified by another session",
			"note":  current,
		})
		return
	}
	if err != nil {
		log.Printf("restore note version %d/%d: %v", noteID, versionID, err)
		http.Error(w, "failed to restore note version", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, note)
}
