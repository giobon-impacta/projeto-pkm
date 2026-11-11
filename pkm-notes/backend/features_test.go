package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAttachmentUploadDownloadAndDelete(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	noteID, err := app.CreateNote(Note{Title: "with attachment"})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler(filepath.Join("..", "web", "frontend"))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "folder\\image.png")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("small image payload")
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/api/notes/"+strconv.FormatInt(noteID, 10)+"/attachments", &body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var item Attachment
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Filename != "image.png" || item.Size != int64(len(content)) || item.NoteID != noteID {
		t.Fatalf("unexpected attachment metadata: %#v", item)
	}
	attachment, filePath, err := app.GetAttachment(noteID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.MediaType != "image/png" {
		t.Fatalf("media type = %q", attachment.MediaType)
	}
	stored, err := os.ReadFile(filePath)
	if err != nil || !bytes.Equal(stored, content) {
		t.Fatalf("attachment was not stored in data directory: content=%q err=%v", stored, err)
	}

	download := httptest.NewRequest(http.MethodGet, item.URL, nil)
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != http.StatusOK || !bytes.Equal(downloadResponse.Body.Bytes(), content) {
		t.Fatalf("download status = %d, body = %q", downloadResponse.Code, downloadResponse.Body.Bytes())
	}
	if !strings.HasPrefix(downloadResponse.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download disposition = %q", downloadResponse.Header().Get("Content-Disposition"))
	}
	inline := httptest.NewRequest(http.MethodGet, item.URL+"?inline=1", nil)
	inlineResponse := httptest.NewRecorder()
	handler.ServeHTTP(inlineResponse, inline)
	if !strings.HasPrefix(inlineResponse.Header().Get("Content-Disposition"), "inline;") {
		t.Fatalf("image inline disposition = %q", inlineResponse.Header().Get("Content-Disposition"))
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, item.URL, nil)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("deleted attachment still exists; stat error = %v", err)
	}
}

func TestDeletingNoteRemovesItsAttachmentFiles(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	noteID, err := app.CreateNote(Note{Title: "delete attachments"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := app.SaveAttachment(noteID, strings.NewReader("payload"), "file.bin")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, "attachments", strconv.FormatInt(noteID, 10))
	if err := app.DeleteNote(noteID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("note attachment directory still exists: %v", err)
	}
	if _, _, err := app.GetAttachment(noteID, item.ID); err == nil {
		t.Fatal("attachment metadata remained after note deletion")
	}
}

func TestNoteGraphAndBacklinksResolveMarkdownAndWikilinks(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	sourceID, err := app.CreateNote(Note{
		Title:  "Source",
		Folder: "Projects",
		Content: "[[Target|alias]] [Other](../Reference/Other.md) " +
			"[external](https://example.com) [protocol-relative](//example.com/Other) " +
			"`[[CodeOnly]]`\n\n```md\n[[AlsoCodeOnly]]\n```",
	})
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := app.CreateNote(Note{Title: "Target", Folder: "Projects"})
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := app.CreateNote(Note{Title: "Other", Folder: "Reference"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateNote(Note{Title: "CodeOnly"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateNote(Note{Title: "AlsoCodeOnly"}); err != nil {
		t.Fatal(err)
	}

	graph, err := app.NoteGraph()
	if err != nil {
		t.Fatal(err)
	}
	targets := make(map[int64]string)
	for _, edge := range graph.Edges {
		if edge.Source == sourceID {
			targets[edge.Target] = edge.Type
		}
	}
	if len(targets) != 2 || targets[targetID] != "wikilink" || targets[otherID] != "markdown" {
		t.Fatalf("unexpected graph edges from source: %#v", graph.Edges)
	}
	backlinks, err := app.Backlinks(targetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(backlinks) != 1 || backlinks[0].ID != sourceID {
		t.Fatalf("unexpected backlinks: %#v", backlinks)
	}
}

func TestPreviewWikilinksBecomeNoteLinksOutsideCode(t *testing.T) {
	source := indexedNote{GraphNode: GraphNode{ID: 1, Title: "Source", Folder: "Projects"}}
	byPath := map[string]int64{
		"projects/target": 2,
		"reference/other": 3,
	}
	byTitle := map[string]int64{"target": 2, "other": 3}
	markdown := "[[Target]] [[Other|Read this]] `[[Target]]`\n\n```md\n[[Target]]\n```\n\n![[Target]] [[Missing]]"
	rendered := convertWikilinksToMarkdown(markdown, source, byPath, byTitle)
	for _, expected := range []string{
		"[Target](?note=2)",
		"[Read this](?note=3)",
		"`[[Target]]`",
		"```md\n[[Target]]\n```",
		"![[Target]]",
		"[[Missing]]",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("preview conversion is missing %q: %s", expected, rendered)
		}
	}
	response := httptest.NewRecorder()
	renderMarkdownPreview(response, []byte(rendered))
	if !strings.Contains(response.Body.String(), `<a href="?note=2">Target</a>`) ||
		!strings.Contains(response.Body.String(), `<a href="?note=3">Read this</a>`) {
		t.Fatalf("wikilinks did not render as clickable note links: %s", response.Body.String())
	}
}

func TestAuthenticationProtectsAPIAndSupportsLoginLogout(t *testing.T) {
	t.Setenv("AUTHENTICATION_PASSWORD", "test-password")
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Handler(filepath.Join("..", "web", "frontend"))

	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusSeeOther || root.Header().Get("Location") != "/login.html" {
		t.Fatalf("unauthenticated root status = %d, location = %q", root.Code, root.Header().Get("Location"))
	}
	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if index.Code != http.StatusSeeOther || index.Header().Get("Location") != "/login.html" {
		t.Fatalf("unauthenticated index.html status = %d, location = %q", index.Code, index.Header().Get("Location"))
	}
	protected := httptest.NewRecorder()
	handler.ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/api/notes", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("protected API status = %d", protected.Code)
	}

	wrong := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"wrong"}`))
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d", wrongResponse.Code)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"test-password"}`))
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body.String())
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie is missing security attributes: %#v", cookies)
	}

	authenticated := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	authenticated.AddCookie(cookies[0])
	authenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(authenticatedResponse, authenticated)
	if authenticatedResponse.Code != http.StatusOK {
		t.Fatalf("authenticated API status = %d, body = %s", authenticatedResponse.Code, authenticatedResponse.Body.String())
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logout.AddCookie(cookies[0])
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK || logoutResponse.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout response = %d, cookies = %#v", logoutResponse.Code, logoutResponse.Result().Cookies())
	}
}

func TestExpiredAndTamperedAuthenticationTokensAreRejected(t *testing.T) {
	auth, err := newAuthenticator("password")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.newSessionToken(time.Now().Add(-authSessionTTL - time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if auth.validSessionToken(token, time.Now()) {
		t.Fatal("expired session token was accepted")
	}
	valid, err := auth.newSessionToken(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if auth.validSessionToken(valid+"tampered", time.Now()) {
		t.Fatal("tampered session token was accepted")
	}
}

func TestPersistedLinksFollowRenamesMovesAndUpdates(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	sourceID, err := app.CreateNote(Note{
		Title:   "Source",
		Folder:  "Projects",
		Content: "[[Target]] [Other](../Reference/Other.md)",
	})
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := app.CreateNote(Note{Title: "Target", Folder: "Projects"})
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := app.CreateNote(Note{Title: "Other", Folder: "Reference"})
	if err != nil {
		t.Fatal(err)
	}
	movedOtherID, err := app.CreateNote(Note{Title: "Other", Folder: "Archive/Reference"})
	if err != nil {
		t.Fatal(err)
	}

	edgesFrom := func(id int64) map[int64]string {
		graph, err := app.NoteGraph()
		if err != nil {
			t.Fatal(err)
		}
		edges := make(map[int64]string)
		for _, edge := range graph.Edges {
			if edge.Source == id {
				edges[edge.Target] = edge.Type
			}
		}
		return edges
	}

	if edges := edgesFrom(sourceID); len(edges) != 2 ||
		edges[targetID] != "wikilink" || edges[otherID] != "markdown" {
		t.Fatalf("unexpected initial edges: %#v", edges)
	}
	backlinks, err := app.Backlinks(targetID)
	if err != nil || len(backlinks) != 1 || backlinks[0].ID != sourceID {
		t.Fatalf("unexpected initial backlinks: %#v err=%v", backlinks, err)
	}

	if err := app.UpdateNote(targetID, Note{Title: "Renamed", Folder: "Projects"}); err != nil {
		t.Fatal(err)
	}
	if edges := edgesFrom(sourceID); len(edges) != 1 || edges[otherID] != "markdown" {
		t.Fatalf("renaming the target should drop the dangling wikilink: %#v", edges)
	}

	newTargetID, err := app.CreateNote(Note{Title: "Target", Folder: "Projects"})
	if err != nil {
		t.Fatal(err)
	}
	if edges := edgesFrom(sourceID); len(edges) != 2 || edges[newTargetID] != "wikilink" {
		t.Fatalf("a new note with the missing title should make the link resolve again: %#v", edges)
	}

	if err := app.MoveNoteFolder(sourceID, "Archive/Deep"); err != nil {
		t.Fatal(err)
	}
	if edges := edgesFrom(sourceID); len(edges) != 2 ||
		edges[newTargetID] != "wikilink" || edges[movedOtherID] != "markdown" {
		t.Fatalf("moving the source should change relative link resolution: %#v", edges)
	}
	if err := app.MoveNoteFolder(sourceID, "Projects"); err != nil {
		t.Fatal(err)
	}
	if edges := edgesFrom(sourceID); len(edges) != 2 ||
		edges[newTargetID] != "wikilink" || edges[otherID] != "markdown" {
		t.Fatalf("moving the source back should restore the original relative target: %#v", edges)
	}

	if err := app.UpdateNote(sourceID, Note{
		Title:   "Source",
		Folder:  "Projects",
		Content: "[[Target]] only",
	}); err != nil {
		t.Fatal(err)
	}
	if edges := edgesFrom(sourceID); len(edges) != 1 || edges[newTargetID] != "wikilink" {
		t.Fatalf("updating the content should remove the markdown edge: %#v", edges)
	}

	if err := app.DeleteNote(newTargetID); err != nil {
		t.Fatal(err)
	}
	if edges := edgesFrom(sourceID); len(edges) != 0 {
		t.Fatalf("deleting the target should remove the edge: %#v", edges)
	}
	var linkRows int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM note_links WHERE source_id = ?", sourceID).Scan(&linkRows); err != nil {
		t.Fatal(err)
	}
	if linkRows != 1 {
		t.Fatalf("expected one stored raw link for the remaining wikilink, got %d", linkRows)
	}
	if err := app.DeleteNote(sourceID); err != nil {
		t.Fatal(err)
	}
	if err := app.db.QueryRow("SELECT COUNT(*) FROM note_links WHERE source_id = ?", sourceID).Scan(&linkRows); err != nil {
		t.Fatal(err)
	}
	if linkRows != 0 {
		t.Fatalf("deleting a note should cascade to its link rows, got %d", linkRows)
	}
}

func TestMigrationBackfillsPersistedLinksIdempotently(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE notes (
		rowid INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		content TEXT,
		folder TEXT DEFAULT '',
		tags TEXT DEFAULT '',
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now'))
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO notes(title, content) VALUES ('Source', '[[Target]]')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO notes(title, content) VALUES ('Target', 'body')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		app, err := NewApp(dataDir)
		if err != nil {
			t.Fatalf("open attempt %d: %v", attempt, err)
		}
		graph, err := app.NoteGraph()
		if err != nil {
			app.Close()
			t.Fatalf("graph attempt %d: %v", attempt, err)
		}
		if len(graph.Edges) != 1 || graph.Edges[0].Type != "wikilink" {
			app.Close()
			t.Fatalf("migration did not backfill links on attempt %d: %#v", attempt, graph.Edges)
		}
		var version int
		if err := app.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
			app.Close()
			t.Fatal(err)
		}
		if version != 2 {
			app.Close()
			t.Fatalf("schema version = %d, want 2", version)
		}
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHealthReportsDatabaseStatus(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Handler(filepath.Join("..", "web", "frontend"))
	okResponse := httptest.NewRecorder()
	handler.ServeHTTP(okResponse, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if okResponse.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, body = %s", okResponse.Code, okResponse.Body.String())
	}
	if err := app.db.Close(); err != nil {
		t.Fatal(err)
	}
	failedResponse := httptest.NewRecorder()
	handler.ServeHTTP(failedResponse, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if failedResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy status = %d, body = %s", failedResponse.Code, failedResponse.Body.String())
	}
}

func TestNoteGraphUsesPersistedLinks(t *testing.T) {
	// Guard against accidentally dropping the persisted-link fast path: the
	// graph must not need note contents once note_links is populated.
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	first, err := app.CreateNote(Note{Title: "A", Content: "[[B]]"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateNote(Note{Title: "B", Content: "[[A]]"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec("UPDATE notes SET content = 'corrupted' WHERE rowid = ?", first); err != nil {
		t.Fatal(err)
	}
	graph, err := app.NoteGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 2 {
		t.Fatalf("graph read note content instead of note_links: %#v", graph.Edges)
	}
}

func BenchmarkNoteGraph(b *testing.B) {
	app, err := NewApp(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer app.Close()
	const noteCount = 2000
	for index := 0; index < noteCount; index++ {
		content := fmt.Sprintf("[[Note %d]] [[Note %d]]", (index+1)%noteCount, (index+7)%noteCount)
		if _, err := app.CreateNote(Note{Title: fmt.Sprintf("Note %d", index), Content: content}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := app.NoteGraph(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestAttachmentRequestRejectsUnknownNote(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "payload")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/notes/999/attachments", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	app.Handler(filepath.Join("..", "web", "frontend")).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing note upload status = %d, body = %s", response.Code, response.Body.String())
	}
}
