package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxAttachmentSize = 25 << 20

var (
	errInvalidAttachmentFilename = errors.New("attachment filename is empty or invalid")
	errAttachmentTooLarge        = fmt.Errorf("attachment exceeds the %d MiB limit", maxAttachmentSize>>20)
)

type Attachment struct {
	ID        int64  `json:"id"`
	NoteID    int64  `json:"note_id"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
	URL       string `json:"url"`
}

func cleanAttachmentFilename(filename string) (string, error) {
	filename = path.Base(strings.ReplaceAll(filename, `\`, "/"))
	filename = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, filename))
	if filename == "" || filename == "." || filename == ".." {
		return "", errInvalidAttachmentFilename
	}
	var safeName strings.Builder
	for _, character := range filename {
		if safeName.Len()+utf8.RuneLen(character) > 255 {
			break
		}
		safeName.WriteRune(character)
	}
	if safeName.Len() == 0 {
		return "", errInvalidAttachmentFilename
	}
	return safeName.String(), nil
}

func (a *App) ListAttachments(noteID int64) ([]Attachment, error) {
	var exists int
	if err := a.db.QueryRow("SELECT 1 FROM notes WHERE rowid = ?", noteID).Scan(&exists); err != nil {
		return nil, err
	}
	rows, err := a.db.Query(`SELECT id, note_id, filename, media_type, size, created_at
		FROM attachments WHERE note_id = ? ORDER BY created_at, id`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Attachment, 0)
	for rows.Next() {
		var item Attachment
		if err := rows.Scan(&item.ID, &item.NoteID, &item.Filename, &item.MediaType, &item.Size, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.URL = attachmentURL(item.NoteID, item.ID)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) SaveAttachment(noteID int64, source io.Reader, originalFilename string) (Attachment, error) {
	var exists int
	if err := a.db.QueryRow("SELECT 1 FROM notes WHERE rowid = ?", noteID).Scan(&exists); err != nil {
		return Attachment{}, err
	}
	filename, err := cleanAttachmentFilename(originalFilename)
	if err != nil {
		return Attachment{}, err
	}
	noteDir := filepath.Join(a.attachmentsDir, strconv.FormatInt(noteID, 10))
	if err := os.MkdirAll(noteDir, 0700); err != nil {
		return Attachment{}, fmt.Errorf("create note attachment directory: %w", err)
	}
	temp, err := os.CreateTemp(noteDir, ".upload-*")
	if err != nil {
		return Attachment{}, fmt.Errorf("create temporary attachment: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	size, copyErr := io.Copy(temp, io.LimitReader(source, maxAttachmentSize+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return Attachment{}, fmt.Errorf("write attachment: %w", copyErr)
	}
	if closeErr != nil {
		return Attachment{}, fmt.Errorf("close attachment: %w", closeErr)
	}
	if size > maxAttachmentSize {
		return Attachment{}, errAttachmentTooLarge
	}
	key, err := randomHex(16)
	if err != nil {
		return Attachment{}, fmt.Errorf("generate attachment ID: %w", err)
	}
	storageName := key
	destination := filepath.Join(noteDir, storageName)
	if err := os.Rename(tempName, destination); err != nil {
		return Attachment{}, fmt.Errorf("store attachment: %w", err)
	}
	mediaType := mime.TypeByExtension(filepath.Ext(filename))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	tx, err := a.db.Begin()
	if err != nil {
		return Attachment{}, removeAttachmentFile(destination, fmt.Errorf("begin attachment metadata transaction: %w", err))
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO attachments(note_id, filename, storage_name, media_type, size)
		VALUES (?, ?, ?, ?, ?)`, noteID, filename, storageName, mediaType, size)
	if err != nil {
		return Attachment{}, removeAttachmentFile(destination, fmt.Errorf("save attachment metadata: %w", err))
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Attachment{}, removeAttachmentFile(destination, fmt.Errorf("read attachment ID: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return Attachment{}, removeAttachmentFile(destination, fmt.Errorf("commit attachment metadata: %w", err))
	}
	return Attachment{
		ID: id, NoteID: noteID, Filename: filename, MediaType: mediaType,
		Size: size, URL: attachmentURL(noteID, id),
	}, nil
}

func removeAttachmentFile(filePath string, cause error) error {
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%v; remove unreferenced file: %w", cause, err)
	}
	return cause
}

func (a *App) GetAttachment(noteID, attachmentID int64) (Attachment, string, error) {
	var item Attachment
	var storageName string
	err := a.db.QueryRow(`SELECT id, note_id, filename, storage_name, media_type, size, created_at
		FROM attachments WHERE note_id = ? AND id = ?`, noteID, attachmentID).
		Scan(&item.ID, &item.NoteID, &item.Filename, &storageName, &item.MediaType, &item.Size, &item.CreatedAt)
	if err != nil {
		return Attachment{}, "", err
	}
	item.URL = attachmentURL(item.NoteID, item.ID)
	return item, filepath.Join(a.attachmentsDir, strconv.FormatInt(noteID, 10), storageName), nil
}

func (a *App) DeleteAttachment(noteID, attachmentID int64) error {
	item, filePath, err := a.GetAttachment(noteID, attachmentID)
	if err != nil {
		return err
	}
	result, err := a.db.Exec("DELETE FROM attachments WHERE id = ? AND note_id = ?", item.ID, noteID)
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
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("attachment metadata deleted but file cleanup failed: %w", err)
	}
	return nil
}

func (a *App) handleAttachments(w http.ResponseWriter, r *http.Request, noteID int64) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.ListAttachments(noteID)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else if err != nil {
			logAttachmentError(w, "list attachments", err)
		} else {
			writeJSON(w, http.StatusOK, items)
		}
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentSize+(1<<20))
		err := r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				http.Error(w, "attachment request is too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "multipart request must include a file field", http.StatusBadRequest)
			}
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "multipart request must include a file field", http.StatusBadRequest)
			return
		}
		defer file.Close()
		item, err := a.SaveAttachment(noteID, file, header.Filename)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else if err != nil {
			if errors.Is(err, errAttachmentTooLarge) {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			} else if errors.Is(err, errInvalidAttachmentFilename) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				logAttachmentError(w, "save attachment", err)
			}
		} else {
			writeJSON(w, http.StatusCreated, item)
		}
	default:
		methodNotAllowed(w)
	}
}

func (a *App) handleAttachment(w http.ResponseWriter, r *http.Request, noteID, attachmentID int64) {
	switch r.Method {
	case http.MethodGet:
		item, filePath, err := a.GetAttachment(noteID, attachmentID)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			logAttachmentError(w, "load attachment", err)
			return
		}
		file, err := os.Open(filePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
			} else {
				logAttachmentError(w, "open attachment", err)
			}
			return
		}
		defer file.Close()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Content-Type", item.MediaType)
		disposition := "attachment"
		if r.URL.Query().Get("inline") == "1" && isSafeInlineImage(item.MediaType) {
			disposition = "inline"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": item.Filename}))
		// Attachment storage names are random and never reused, so the content
		// behind a given ID is immutable. Keep it private because downloads may
		// require an authenticated session.
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		http.ServeContent(w, r, item.Filename, time.Time{}, file)
	case http.MethodDelete:
		err := a.DeleteAttachment(noteID, attachmentID)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else if err != nil {
			logAttachmentError(w, "delete attachment", err)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		methodNotAllowed(w)
	}
}

func attachmentURL(noteID, attachmentID int64) string {
	return fmt.Sprintf("/api/notes/%d/attachments/%d", noteID, attachmentID)
}

func isSafeInlineImage(mediaType string) bool {
	switch strings.ToLower(mediaType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif":
		return true
	default:
		return false
	}
}

func logAttachmentError(w http.ResponseWriter, operation string, err error) {
	log.Printf("%s: %v", operation, err)
	http.Error(w, "failed to "+operation, http.StatusInternalServerError)
}
