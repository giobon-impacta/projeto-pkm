package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	backupFormat        = "pkm-notes-backup"
	backupVersion       = 1
	maxBackupUpload     = 512 << 20
	maxBackupManifest   = 512 << 20
	maxBackupNotes      = 100000
	maxBackupAttachment = maxAttachmentSize
)

var errInvalidBackup = errors.New("invalid backup archive")

type backupManifest struct {
	Format      string              `json:"format"`
	Version     int                 `json:"version"`
	CreatedAt   string              `json:"created_at"`
	Notes       []backupNote        `json:"notes"`
	Folders     []string            `json:"folders,omitempty"`
	Versions    []backupNoteVersion `json:"versions,omitempty"`
	Attachments []backupAttachment  `json:"attachments"`
}

type backupNote struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Folder    string   `json:"folder"`
	Tags      []string `json:"tags,omitempty"`
	Version   int64    `json:"version"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type backupAttachment struct {
	ID          int64  `json:"id"`
	NoteID      int64  `json:"note_id"`
	Filename    string `json:"filename"`
	StorageName string `json:"storage_name"`
	MediaType   string `json:"media_type"`
	Size        int64  `json:"size"`
	CreatedAt   string `json:"created_at"`
}

type backupNoteVersion struct {
	ID        int64    `json:"id"`
	NoteID    int64    `json:"note_id"`
	Version   int64    `json:"version"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Folder    string   `json:"folder"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// ExportBackup writes a logical archive with a manifest and every attachment
// file. Note IDs are preserved so attachment URLs stored in note content keep
// working after a restore.
func (a *App) ExportBackup(destination io.Writer) error {
	manifest := backupManifest{
		Format:    backupFormat,
		Version:   backupVersion,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	rows, err := a.db.Query(`SELECT rowid, title, COALESCE(content, ''), COALESCE(folder, ''),
		COALESCE(tags, ''), version, created_at, updated_at FROM notes ORDER BY rowid`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var note backupNote
		var tags string
		if err := rows.Scan(&note.ID, &note.Title, &note.Content, &note.Folder,
			&tags, &note.Version, &note.CreatedAt, &note.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		for _, tag := range strings.Split(tags, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				note.Tags = append(note.Tags, tag)
			}
		}
		manifest.Notes = append(manifest.Notes, note)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	rows, err = a.db.Query("SELECT path FROM folders ORDER BY path")
	if err != nil {
		return err
	}
	for rows.Next() {
		var folder string
		if err := rows.Scan(&folder); err != nil {
			rows.Close()
			return err
		}
		manifest.Folders = append(manifest.Folders, folder)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	rows, err = a.db.Query(`SELECT id, note_id, version, title, COALESCE(content, ''),
		COALESCE(folder, ''), COALESCE(tags, ''), created_at
		FROM note_versions ORDER BY note_id, id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var version backupNoteVersion
		var tags string
		if err := rows.Scan(&version.ID, &version.NoteID, &version.Version, &version.Title,
			&version.Content, &version.Folder, &tags, &version.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		for _, tag := range strings.Split(tags, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				version.Tags = append(version.Tags, tag)
			}
		}
		manifest.Versions = append(manifest.Versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	rows, err = a.db.Query(`SELECT id, note_id, filename, storage_name, media_type, size, created_at
		FROM attachments ORDER BY note_id, id`)
	if err != nil {
		return err
	}
	type attachmentFile struct {
		meta backupAttachment
		path string
	}
	files := make([]attachmentFile, 0)
	for rows.Next() {
		var item backupAttachment
		if err := rows.Scan(&item.ID, &item.NoteID, &item.Filename, &item.StorageName,
			&item.MediaType, &item.Size, &item.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		filePath := filepath.Join(a.attachmentsDir, fmt.Sprint(item.NoteID), item.StorageName)
		if _, err := os.Stat(filePath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				log.Printf("backup: skipping missing attachment %d (%s)", item.ID, filePath)
				continue
			}
			rows.Close()
			return err
		}
		manifest.Attachments = append(manifest.Attachments, item)
		files = append(files, attachmentFile{meta: item, path: filePath})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	archive := zip.NewWriter(destination)
	manifestWriter, err := archive.CreateHeader(&zip.FileHeader{
		Name:     "manifest.json",
		Method:   zip.Deflate,
		Modified: time.Now(),
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(manifestWriter)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		return err
	}
	for _, file := range files {
		header := &zip.FileHeader{
			Name:     backupAttachmentPath(file.meta.NoteID, file.meta.StorageName),
			Method:   zip.Deflate,
			Modified: time.Now(),
		}
		header.SetMode(0600)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		source, err := os.Open(file.path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, source)
		closeErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return archive.Close()
}

func backupAttachmentPath(noteID int64, storageName string) string {
	return fmt.Sprintf("attachments/%d/%s", noteID, storageName)
}

// RestoreBackup replaces the whole library with the archive contents. All
// validation happens before the database and attachment directory are touched,
// so a malformed archive leaves the current data intact.
func (a *App) RestoreBackup(data []byte) (int, int, error) {
	manifest, files, err := readBackupArchive(data)
	if err != nil {
		return 0, 0, err
	}

	staging, err := os.MkdirTemp(filepath.Dir(a.attachmentsDir), ".restore-*")
	if err != nil {
		return 0, 0, fmt.Errorf("create restore staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := writeRestoreFiles(staging, manifest.Attachments, files); err != nil {
		return 0, 0, err
	}

	restoreAttachments, cleanupAttachments, err := swapAttachmentsDir(a.attachmentsDir, staging)
	if err != nil {
		return 0, 0, err
	}
	committed := false
	defer func() {
		if !committed {
			restoreAttachments()
		}
	}()

	tx, err := a.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM notes"); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM folders"); err != nil {
		return 0, 0, err
	}
	if err := insertRestoredNotes(tx, manifest.Notes); err != nil {
		return 0, 0, err
	}
	if err := insertRestoredFolders(tx, manifest.Folders); err != nil {
		return 0, 0, err
	}
	if err := insertRestoredVersions(tx, manifest.Versions); err != nil {
		return 0, 0, err
	}
	if err := insertRestoredAttachments(tx, manifest.Attachments); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	committed = true
	cleanupAttachments()
	return len(manifest.Notes), len(manifest.Attachments), nil
}

func readBackupArchive(data []byte) (backupManifest, map[string]*zip.File, error) {
	if len(data) > maxBackupUpload {
		return backupManifest{}, nil, fmt.Errorf("%w: archive exceeds the %d MiB limit",
			errInvalidBackup, maxBackupUpload>>20)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return backupManifest{}, nil, fmt.Errorf("%w: %v", errInvalidBackup, err)
	}
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		if _, exists := files[file.Name]; exists {
			return backupManifest{}, nil, fmt.Errorf("%w: duplicate entry %q", errInvalidBackup, file.Name)
		}
		files[file.Name] = file
	}
	manifestFile, exists := files["manifest.json"]
	if !exists {
		return backupManifest{}, nil, fmt.Errorf("%w: missing manifest.json", errInvalidBackup)
	}
	if manifestFile.UncompressedSize64 > maxBackupManifest {
		return backupManifest{}, nil, fmt.Errorf("%w: manifest is too large", errInvalidBackup)
	}
	manifestReader, err := manifestFile.Open()
	if err != nil {
		return backupManifest{}, nil, err
	}
	var manifest backupManifest
	decodeErr := json.NewDecoder(io.LimitReader(manifestReader, maxBackupManifest)).Decode(&manifest)
	closeErr := manifestReader.Close()
	if decodeErr != nil {
		return backupManifest{}, nil, fmt.Errorf("%w: %v", errInvalidBackup, decodeErr)
	}
	if closeErr != nil {
		return backupManifest{}, nil, closeErr
	}
	if manifest.Format != backupFormat || manifest.Version != backupVersion {
		return backupManifest{}, nil, fmt.Errorf("%w: unsupported format or version", errInvalidBackup)
	}
	if len(manifest.Notes) > maxBackupNotes {
		return backupManifest{}, nil, fmt.Errorf("%w: too many notes", errInvalidBackup)
	}

	seenFolders := make(map[string]struct{}, len(manifest.Folders))
	for index, folder := range manifest.Folders {
		normalized, err := normalizeFolder(folder)
		if err != nil || normalized != folder || folder == "" {
			return backupManifest{}, nil, fmt.Errorf("%w: folder %d has an invalid path", errInvalidBackup, index)
		}
		if _, exists := seenFolders[folder]; exists {
			return backupManifest{}, nil, fmt.Errorf("%w: duplicate folder %q", errInvalidBackup, folder)
		}
		seenFolders[folder] = struct{}{}
	}

	noteIDs := make(map[int64]struct{}, len(manifest.Notes))
	for index := range manifest.Notes {
		note := &manifest.Notes[index]
		if note.ID < 1 {
			return backupManifest{}, nil, fmt.Errorf("%w: note %d has an invalid ID", errInvalidBackup, index)
		}
		if _, exists := noteIDs[note.ID]; exists {
			return backupManifest{}, nil, fmt.Errorf("%w: duplicate note ID %d", errInvalidBackup, note.ID)
		}
		noteIDs[note.ID] = struct{}{}
		candidate := Note{
			Title: note.Title, Content: note.Content, Folder: note.Folder, Tags: note.Tags,
		}
		if err := validateNote(&candidate); err != nil {
			return backupManifest{}, nil, fmt.Errorf("%w: note %d: %v", errInvalidBackup, note.ID, err)
		}
		note.Title = candidate.Title
		note.Folder = candidate.Folder
		note.Tags = candidate.Tags
		if note.Version < 1 {
			note.Version = 1
		}
	}

	versionIDs := make(map[int64]struct{}, len(manifest.Versions))
	versionNumbers := make(map[string]struct{}, len(manifest.Versions))
	for index := range manifest.Versions {
		version := &manifest.Versions[index]
		if version.ID < 1 {
			return backupManifest{}, nil, fmt.Errorf("%w: version %d has an invalid ID", errInvalidBackup, index)
		}
		if _, exists := versionIDs[version.ID]; exists {
			return backupManifest{}, nil, fmt.Errorf("%w: duplicate version ID %d", errInvalidBackup, version.ID)
		}
		versionIDs[version.ID] = struct{}{}
		if _, exists := noteIDs[version.NoteID]; !exists {
			return backupManifest{}, nil, fmt.Errorf("%w: version %d references an unknown note",
				errInvalidBackup, version.ID)
		}
		if version.Version < 1 {
			return backupManifest{}, nil, fmt.Errorf("%w: version %d has an invalid number",
				errInvalidBackup, version.ID)
		}
		key := fmt.Sprintf("%d/%d", version.NoteID, version.Version)
		if _, exists := versionNumbers[key]; exists {
			return backupManifest{}, nil, fmt.Errorf("%w: duplicate snapshot %q", errInvalidBackup, key)
		}
		versionNumbers[key] = struct{}{}
		candidate := Note{
			Title: version.Title, Content: version.Content, Folder: version.Folder, Tags: version.Tags,
		}
		if err := validateNote(&candidate); err != nil {
			return backupManifest{}, nil, fmt.Errorf("%w: version %d: %v", errInvalidBackup, version.ID, err)
		}
		version.Title = candidate.Title
		version.Folder = candidate.Folder
		version.Tags = candidate.Tags
	}

	attachmentIDs := make(map[int64]struct{}, len(manifest.Attachments))
	storageNames := make(map[string]struct{}, len(manifest.Attachments))
	for _, attachment := range manifest.Attachments {
		if attachment.ID < 1 {
			return backupManifest{}, nil, fmt.Errorf("%w: attachment has an invalid ID", errInvalidBackup)
		}
		if _, exists := attachmentIDs[attachment.ID]; exists {
			return backupManifest{}, nil, fmt.Errorf("%w: duplicate attachment ID %d", errInvalidBackup, attachment.ID)
		}
		attachmentIDs[attachment.ID] = struct{}{}
		if _, exists := noteIDs[attachment.NoteID]; !exists {
			return backupManifest{}, nil, fmt.Errorf("%w: attachment %d references an unknown note",
				errInvalidBackup, attachment.ID)
		}
		filename, err := cleanAttachmentFilename(attachment.Filename)
		if err != nil || filename != attachment.Filename {
			return backupManifest{}, nil, fmt.Errorf("%w: attachment %d has an invalid filename",
				errInvalidBackup, attachment.ID)
		}
		if !validStorageName(attachment.StorageName) {
			return backupManifest{}, nil, fmt.Errorf("%w: attachment %d has an invalid storage name",
				errInvalidBackup, attachment.ID)
		}
		key := fmt.Sprintf("%d/%s", attachment.NoteID, attachment.StorageName)
		if _, exists := storageNames[key]; exists {
			return backupManifest{}, nil, fmt.Errorf("%w: duplicate attachment storage %q", errInvalidBackup, key)
		}
		storageNames[key] = struct{}{}
		if attachment.Size < 0 || attachment.Size > maxBackupAttachment {
			return backupManifest{}, nil, fmt.Errorf("%w: attachment %d has an invalid size",
				errInvalidBackup, attachment.ID)
		}
		if strings.TrimSpace(attachment.MediaType) == "" ||
			strings.ContainsAny(attachment.MediaType, "\r\n\x00") {
			return backupManifest{}, nil, fmt.Errorf("%w: attachment %d has an invalid media type",
				errInvalidBackup, attachment.ID)
		}
		entry, exists := files[backupAttachmentPath(attachment.NoteID, attachment.StorageName)]
		if !exists {
			return backupManifest{}, nil, fmt.Errorf("%w: missing file for attachment %d",
				errInvalidBackup, attachment.ID)
		}
		if entry.UncompressedSize64 > maxBackupAttachment {
			return backupManifest{}, nil, fmt.Errorf("%w: attachment %d is too large",
				errInvalidBackup, attachment.ID)
		}
	}
	return manifest, files, nil
}

func insertRestoredFolders(tx *sql.Tx, folders []string) error {
	for _, folder := range folders {
		if err := insertFolderPath(tx, folder); err != nil {
			return fmt.Errorf("restore folder %q: %w", folder, err)
		}
	}
	return nil
}

func validStorageName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) || path.Base(name) != name {
		return false
	}
	return true
}

func writeRestoreFiles(staging string, attachments []backupAttachment, files map[string]*zip.File) error {
	for _, attachment := range attachments {
		entry := files[backupAttachmentPath(attachment.NoteID, attachment.StorageName)]
		source, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open attachment %d: %w", attachment.ID, err)
		}
		noteDir := filepath.Join(staging, fmt.Sprint(attachment.NoteID))
		if err := os.MkdirAll(noteDir, 0700); err != nil {
			source.Close()
			return fmt.Errorf("create attachment directory: %w", err)
		}
		destination := filepath.Join(noteDir, attachment.StorageName)
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			source.Close()
			return fmt.Errorf("create restored attachment: %w", err)
		}
		size, copyErr := io.Copy(file, io.LimitReader(source, maxBackupAttachment+1))
		closeErr := file.Close()
		sourceErr := source.Close()
		if copyErr != nil {
			return fmt.Errorf("restore attachment %d: %w", attachment.ID, copyErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if sourceErr != nil {
			return sourceErr
		}
		if size != attachment.Size {
			return fmt.Errorf("%w: attachment %d has size %d, expected %d",
				errInvalidBackup, attachment.ID, size, attachment.Size)
		}
	}
	return nil
}

// swapAttachmentsDir replaces the live directory with the staged one. It
// returns a rollback function for a failed database commit and a cleanup
// function that removes the previous directory after a successful restore.
func swapAttachmentsDir(current, staged string) (func(), func(), error) {
	previous := current + ".previous"
	if err := os.RemoveAll(previous); err != nil {
		return nil, nil, err
	}
	hasPrevious := false
	if _, err := os.Stat(current); err == nil {
		if err := os.Rename(current, previous); err != nil {
			return nil, nil, err
		}
		hasPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if err := os.Rename(staged, current); err != nil {
		if hasPrevious {
			_ = os.Rename(previous, current)
		}
		return nil, nil, err
	}
	rollback := func() {
		_ = os.RemoveAll(current)
		if hasPrevious {
			_ = os.Rename(previous, current)
		}
	}
	cleanup := func() {
		if hasPrevious {
			_ = os.RemoveAll(previous)
		}
	}
	return rollback, cleanup, nil
}

func insertRestoredNotes(tx *sql.Tx, notes []backupNote) error {
	for _, note := range notes {
		createdAt := strings.TrimSpace(note.CreatedAt)
		updatedAt := strings.TrimSpace(note.UpdatedAt)
		if _, err := tx.Exec(`INSERT INTO notes(rowid, title, content, folder, tags, version, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), datetime('now')),
				COALESCE(NULLIF(?, ''), datetime('now')))`,
			note.ID, note.Title, note.Content, note.Folder, strings.Join(note.Tags, ","),
			note.Version, createdAt, updatedAt); err != nil {
			return fmt.Errorf("restore note %d: %w", note.ID, err)
		}
		if err := replaceNoteLinks(tx, note.ID, note.Content); err != nil {
			return err
		}
	}
	return nil
}

func insertRestoredVersions(tx *sql.Tx, versions []backupNoteVersion) error {
	for _, version := range versions {
		createdAt := strings.TrimSpace(version.CreatedAt)
		if _, err := tx.Exec(`INSERT INTO note_versions(id, note_id, version, title, content, folder, tags, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), datetime('now')))`,
			version.ID, version.NoteID, version.Version, version.Title, version.Content,
			version.Folder, strings.Join(version.Tags, ","), createdAt); err != nil {
			return fmt.Errorf("restore version %d: %w", version.ID, err)
		}
	}
	return nil
}

func insertRestoredAttachments(tx *sql.Tx, attachments []backupAttachment) error {
	for _, attachment := range attachments {
		createdAt := strings.TrimSpace(attachment.CreatedAt)
		if _, err := tx.Exec(`INSERT INTO attachments(id, note_id, filename, storage_name, media_type, size, created_at)
			VALUES (?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), datetime('now')))`,
			attachment.ID, attachment.NoteID, attachment.Filename, attachment.StorageName,
			attachment.MediaType, attachment.Size, createdAt); err != nil {
			return fmt.Errorf("restore attachment %d: %w", attachment.ID, err)
		}
	}
	return nil
}

func (a *App) handleBackup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.backupMu.Lock()
		defer a.backupMu.Unlock()
		temp, err := os.CreateTemp("", "pkm-notes-backup-*.zip")
		if err != nil {
			log.Printf("create backup: %v", err)
			http.Error(w, "failed to create backup", http.StatusInternalServerError)
			return
		}
		tempName := temp.Name()
		defer os.Remove(tempName)
		if err := a.ExportBackup(temp); err != nil {
			temp.Close()
			log.Printf("export backup: %v", err)
			http.Error(w, "failed to create backup", http.StatusInternalServerError)
			return
		}
		if _, err := temp.Seek(0, io.SeekStart); err != nil {
			temp.Close()
			log.Printf("rewind backup: %v", err)
			http.Error(w, "failed to create backup", http.StatusInternalServerError)
			return
		}
		defer temp.Close()
		info, err := temp.Stat()
		if err != nil {
			log.Printf("stat backup: %v", err)
			http.Error(w, "failed to create backup", http.StatusInternalServerError)
			return
		}
		filename := "pkm-notes-" + time.Now().UTC().Format("20060102-150405") + ".zip"
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		http.ServeContent(w, r, filename, info.ModTime(), temp)
	case http.MethodPost:
		a.backupMu.Lock()
		defer a.backupMu.Unlock()
		r.Body = http.MaxBytesReader(w, r.Body, maxBackupUpload)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				http.Error(w, "backup archive is too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "failed to read backup archive", http.StatusBadRequest)
			}
			return
		}
		notes, attachments, err := a.RestoreBackup(data)
		if errors.Is(err, errInvalidBackup) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			log.Printf("restore backup: %v", err)
			http.Error(w, "failed to restore backup", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"notes": notes, "attachments": attachments})
	default:
		methodNotAllowed(w)
	}
}
