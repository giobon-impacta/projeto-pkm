package main

import (
	"bytes"
	"encoding/json"
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
			"[external](https://example.com) `[[CodeOnly]]`\n\n```md\n[[AlsoCodeOnly]]\n```",
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
