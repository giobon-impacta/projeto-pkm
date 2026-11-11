package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
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
	source, err := app.GetNote(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Content != "[[Renamed]] [Other](../Reference/Other.md)" {
		t.Fatalf("rename did not rewrite the incoming wikilink: %q", source.Content)
	}
	if edges := edgesFrom(sourceID); len(edges) != 2 ||
		edges[targetID] != "wikilink" || edges[otherID] != "markdown" {
		t.Fatalf("renaming the target should keep its wikilink: %#v", edges)
	}
	backlinks, err = app.Backlinks(targetID)
	if err != nil || len(backlinks) != 1 || backlinks[0].ID != sourceID {
		t.Fatalf("backlinks after rename: %#v err=%v", backlinks, err)
	}

	newTargetID, err := app.CreateNote(Note{Title: "Target", Folder: "Projects"})
	if err != nil {
		t.Fatal(err)
	}
	if edges := edgesFrom(sourceID); len(edges) != 2 ||
		edges[targetID] != "wikilink" || edges[otherID] != "markdown" {
		t.Fatalf("the rewritten link must not resolve to another note: %#v", edges)
	}

	if err := app.MoveNoteFolder(sourceID, "Archive/Deep"); err != nil {
		t.Fatal(err)
	}
	source, err = app.GetNote(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Content != "[[Renamed]] [Other](../../Reference/Other.md)" {
		t.Fatalf("moving the source did not rebase its relative markdown link: %q", source.Content)
	}
	if edges := edgesFrom(sourceID); len(edges) != 2 ||
		edges[targetID] != "wikilink" || edges[otherID] != "markdown" || edges[movedOtherID] != "" {
		t.Fatalf("moving the source should preserve the original markdown target: %#v", edges)
	}
	if err := app.MoveNoteFolder(sourceID, "Projects"); err != nil {
		t.Fatal(err)
	}
	source, err = app.GetNote(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Content != "[[Renamed]] [Other](../Reference/Other.md)" {
		t.Fatalf("moving the source back did not restore its relative markdown link: %q", source.Content)
	}
	if edges := edgesFrom(sourceID); len(edges) != 2 ||
		edges[targetID] != "wikilink" || edges[otherID] != "markdown" {
		t.Fatalf("moving the source back should keep the original relative target: %#v", edges)
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

func TestRenamePropagatesToWikilinkVariantsAndMarkdownLinks(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	targetID, err := app.CreateNote(Note{Title: "Target", Folder: "Projects"})
	if err != nil {
		t.Fatal(err)
	}
	// Same title in another folder: path-based links must keep pointing here.
	archiveTargetID, err := app.CreateNote(Note{Title: "Target", Folder: "Archive"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := app.CreateNote(Note{
		Title:  "Source",
		Folder: "Projects",
		Content: strings.Join([]string{
			"[[Target]]",
			"[[Target|alias]]",
			"[[Projects/Target]]",
			"[[Target.md]]",
			"[[Target#Section]]",
			"[[Archive/Target]]",
			"`[[Target]]`",
			"```",
			"[[Target]]",
			"```",
		}, "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := app.UpdateNote(targetID, Note{Title: "Renamed Note", Folder: "Projects"}); err != nil {
		t.Fatal(err)
	}
	source, err := app.GetNote(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"[[Renamed Note]]",
		"[[Renamed Note|alias]]",
		"[[Projects/Renamed Note]]",
		"[[Renamed Note.md]]",
		"[[Renamed Note#Section]]",
		"[[Archive/Target]]",
		"`[[Target]]`",
		"```",
		"[[Target]]",
		"```",
	}, "\n")
	if source.Content != want {
		t.Fatalf("unexpected renamed content:\n got: %q\nwant: %q", source.Content, want)
	}

	graph, err := app.NoteGraph()
	if err != nil {
		t.Fatal(err)
	}
	edges := make(map[int64]string)
	for _, edge := range graph.Edges {
		if edge.Source == sourceID {
			edges[edge.Target] = edge.Type
		}
	}
	if len(edges) != 2 || edges[targetID] != "wikilink" || edges[archiveTargetID] != "wikilink" {
		t.Fatalf("unexpected edges after rename: %#v", edges)
	}
	backlinks, err := app.Backlinks(targetID)
	if err != nil || len(backlinks) != 1 || backlinks[0].ID != sourceID {
		t.Fatalf("unexpected backlinks after rename: %#v err=%v", backlinks, err)
	}

	otherID, err := app.CreateNote(Note{Title: "Other", Folder: "Reference"})
	if err != nil {
		t.Fatal(err)
	}
	markdownSourceID, err := app.CreateNote(Note{
		Title:  "Markdown Source",
		Folder: "Notes",
		Content: `[other](../Reference/Other.md) ![other](../Reference/Other.md "title")
[ref]: ../Reference/Other.md`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.UpdateNote(otherID, Note{Title: "Outra Nota", Folder: "Reference"}); err != nil {
		t.Fatal(err)
	}
	markdownSource, err := app.GetNote(markdownSourceID)
	if err != nil {
		t.Fatal(err)
	}
	wantMarkdown := `[other](../Reference/Outra%20Nota.md) ![other](../Reference/Outra%20Nota.md "title")
[ref]: ../Reference/Outra%20Nota.md`
	if markdownSource.Content != wantMarkdown {
		t.Fatalf("unexpected renamed markdown links:\n got: %q\nwant: %q", markdownSource.Content, wantMarkdown)
	}
	backlinks, err = app.Backlinks(otherID)
	if err != nil || len(backlinks) != 1 || backlinks[0].ID != markdownSourceID {
		t.Fatalf("unexpected markdown backlinks after rename: %#v err=%v", backlinks, err)
	}
}

func TestMovingNoteRebasesIncomingAndOutgoingLinks(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	targetID, err := app.CreateNote(Note{Title: "Moved", Folder: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := app.CreateNote(Note{
		Title:  "Source",
		Folder: "Notes",
		Content: strings.Join([]string{
			"[[Moved]]",
			"[moved](Moved.md)",
			"[outside](../Root/Outside.md)",
		}, "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	numericID, err := app.CreateNote(Note{Title: "2"})
	if err != nil {
		t.Fatal(err)
	}
	outsideID, err := app.CreateNote(Note{
		Title:  "Outside",
		Folder: "Root",
		Content: strings.Join([]string{
			"[moved](../Notes/Moved.md)",
			"[[/Notes/Moved|alias]]",
			"[file](/api/notes/1/attachments/2)",
		}, "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := app.MoveNoteFolder(targetID, "Archive"); err != nil {
		t.Fatal(err)
	}
	outside, err := app.GetNote(outsideID)
	if err != nil {
		t.Fatal(err)
	}
	wantOutside := strings.Join([]string{
		"[moved](../Archive/Moved.md)",
		"[[/Archive/Moved|alias]]",
		"[file](/api/notes/1/attachments/2)",
	}, "\n")
	if outside.Content != wantOutside {
		t.Fatalf("incoming links were not rebased:\n got: %q\nwant: %q", outside.Content, wantOutside)
	}
	source, err := app.GetNote(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	wantSource := strings.Join([]string{
		"[[Moved]]",
		"[moved](Moved.md)",
		"[outside](../Root/Outside.md)",
	}, "\n")
	if source.Content != wantSource {
		t.Fatalf("title-only links should stay unchanged:\n got: %q\nwant: %q", source.Content, wantSource)
	}

	if err := app.MoveNoteFolder(sourceID, "Archive/Deep"); err != nil {
		t.Fatal(err)
	}
	source, err = app.GetNote(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	wantSource = strings.Join([]string{
		"[[Moved]]",
		"[moved](Moved.md)",
		"[outside](../../Root/Outside.md)",
	}, "\n")
	if source.Content != wantSource {
		t.Fatalf("outgoing links were not rebased:\n got: %q\nwant: %q", source.Content, wantSource)
	}

	graph, err := app.NoteGraph()
	if err != nil {
		t.Fatal(err)
	}
	edges := make(map[[2]int64]map[string]bool)
	for _, edge := range graph.Edges {
		key := [2]int64{edge.Source, edge.Target}
		if edges[key] == nil {
			edges[key] = make(map[string]bool)
		}
		edges[key][edge.Type] = true
	}
	if !edges[[2]int64{sourceID, targetID}]["wikilink"] ||
		!edges[[2]int64{sourceID, outsideID}]["markdown"] ||
		!edges[[2]int64{outsideID, targetID}]["markdown"] ||
		!edges[[2]int64{outsideID, targetID}]["wikilink"] {
		t.Fatalf("unexpected edges after moves: %#v", graph.Edges)
	}
	if edges[[2]int64{outsideID, numericID}] != nil {
		t.Fatalf("attachment URL was resolved as a note: %#v", graph.Edges)
	}
}

func TestMovingFolderTreeRebasesLinks(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	aID, err := app.CreateNote(Note{
		Title:  "A",
		Folder: "Projects",
		Content: strings.Join([]string{
			"[[/Projects/B]]",
			"[b](/Projects/B.md)",
			"[outside](../Outside.md)",
		}, "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	bID, err := app.CreateNote(Note{
		Title:  "B",
		Folder: "Projects",
		Content: strings.Join([]string{
			"[a](A.md)",
			"[a-abs](/Projects/A.md)",
		}, "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	outsideID, err := app.CreateNote(Note{
		Title:  "Outside",
		Folder: "",
		Content: strings.Join([]string{
			"[a](Projects/A.md)",
			"[b](/Projects/B.md)",
		}, "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	destination, moved, err := app.MoveFolder("Projects", "Archive")
	if err != nil {
		t.Fatal(err)
	}
	if destination != "Archive/Projects" || moved != 2 {
		t.Fatalf("unexpected move result: destination=%q moved=%d", destination, moved)
	}

	a, err := app.GetNote(aID)
	if err != nil {
		t.Fatal(err)
	}
	wantA := strings.Join([]string{
		"[[/Archive/Projects/B]]",
		"[b](/Archive/Projects/B.md)",
		"[outside](../../Outside.md)",
	}, "\n")
	if a.Content != wantA {
		t.Fatalf("unexpected moved A content:\n got: %q\nwant: %q", a.Content, wantA)
	}

	b, err := app.GetNote(bID)
	if err != nil {
		t.Fatal(err)
	}
	wantB := strings.Join([]string{
		"[a](A.md)",
		"[a-abs](/Archive/Projects/A.md)",
	}, "\n")
	if b.Content != wantB {
		t.Fatalf("unexpected moved B content:\n got: %q\nwant: %q", b.Content, wantB)
	}

	outside, err := app.GetNote(outsideID)
	if err != nil {
		t.Fatal(err)
	}
	wantOutside := strings.Join([]string{
		"[a](Archive/Projects/A.md)",
		"[b](/Archive/Projects/B.md)",
	}, "\n")
	if outside.Content != wantOutside {
		t.Fatalf("unexpected outside content:\n got: %q\nwant: %q", outside.Content, wantOutside)
	}

	graph, err := app.NoteGraph()
	if err != nil {
		t.Fatal(err)
	}
	edges := make(map[[2]int64]bool)
	for _, edge := range graph.Edges {
		edges[[2]int64{edge.Source, edge.Target}] = true
	}
	for _, pair := range [][2]int64{{aID, bID}, {aID, outsideID}, {bID, aID}, {outsideID, aID}, {outsideID, bID}} {
		if !edges[pair] {
			t.Fatalf("missing edge %v after folder move: %#v", pair, graph.Edges)
		}
	}
}

func TestUpdatingNoteFolderRebasesIncomingLinks(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	targetID, err := app.CreateNote(Note{Title: "Moved", Folder: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := app.CreateNote(Note{
		Title:  "Source",
		Folder: "Root",
		Content: strings.Join([]string{
			"[[Moved]]",
			"[moved](Notes/Moved.md)",
			"[[/Notes/Moved|alias]]",
		}, "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// A PUT that renames and moves the note in one request must rewrite the
	// incoming links using both the new title and the new folder.
	if err := app.UpdateNote(targetID, Note{Title: "Renamed", Folder: "Archive"}); err != nil {
		t.Fatal(err)
	}
	source, err := app.GetNote(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"[[Renamed]]",
		"[moved](../Archive/Renamed.md)",
		"[[/Archive/Renamed|alias]]",
	}, "\n")
	if source.Content != want {
		t.Fatalf("unexpected relinked content:\n got: %q\nwant: %q", source.Content, want)
	}

	graph, err := app.NoteGraph()
	if err != nil {
		t.Fatal(err)
	}
	edges := make(map[[2]int64]map[string]bool)
	for _, edge := range graph.Edges {
		key := [2]int64{edge.Source, edge.Target}
		if edges[key] == nil {
			edges[key] = make(map[string]bool)
		}
		edges[key][edge.Type] = true
	}
	if !edges[[2]int64{sourceID, targetID}]["wikilink"] ||
		!edges[[2]int64{sourceID, targetID}]["markdown"] {
		t.Fatalf("unexpected edges after update: %#v", graph.Edges)
	}
}

func TestTagFilteringAndTagList(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	creates := []Note{
		{Title: "Go basics", Tags: []string{"go", "backend"}},
		{Title: "Go patterns", Tags: []string{"Go", "work"}},
		{Title: "Workshop", Tags: []string{"workshop"}},
		{Title: "Misc", Tags: []string{"backend"}},
	}
	for _, note := range creates {
		if _, err := app.CreateNote(note); err != nil {
			t.Fatal(err)
		}
	}

	page, err := app.ListNotes("", "go", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("tag filter is not exact and case-insensitive: %#v", page)
	}
	page, err = app.ListNotes("", "workshop", 30, 0)
	if err != nil || page.Total != 1 || page.Items[0].Title != "Workshop" {
		t.Fatalf("tag filter matched a partial tag: %#v err=%v", page, err)
	}
	results, err := app.SearchNotes("go", "title", "", "backend", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if results.Total != 1 || results.Items[0].Title != "Go basics" {
		t.Fatalf("search did not combine query and tag: %#v", results)
	}

	tags, err := app.Tags()
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int, len(tags))
	for _, tag := range tags {
		counts[strings.ToLower(tag.Name)] = tag.Count
	}
	if len(tags) != 4 || counts["go"] != 2 || counts["backend"] != 2 ||
		counts["work"] != 1 || counts["workshop"] != 1 {
		t.Fatalf("unexpected tag counts: %#v", tags)
	}
	if tags[0].Count != 2 || tags[len(tags)-1].Count != 1 {
		t.Fatalf("tags are not sorted by count: %#v", tags)
	}

	handler := app.Handler(filepath.Join("..", "web", "frontend"))
	request := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("tags endpoint status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload []TagCount
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 4 || payload[0].Count != 2 {
		t.Fatalf("unexpected tags payload: %#v", payload)
	}

	page, err = app.ListNotes("", "does-not-exist", 30, 0)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("unknown tag should return an empty page: %#v err=%v", page, err)
	}
}

func TestBackupRoundTripRestoresNotesAndAttachments(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	targetID, err := app.CreateNote(Note{Title: "Target", Folder: "Notes", Tags: []string{"go", "Backup"}})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := app.CreateNote(Note{
		Title: "Source", Folder: "Notes", Content: "[[Target]]", Tags: []string{"kb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := app.SaveAttachment(targetID, strings.NewReader("attachment bytes"), "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	var attachmentStorageName string
	if err := app.db.QueryRow("SELECT storage_name FROM attachments WHERE id = ?", attachment.ID).
		Scan(&attachmentStorageName); err != nil {
		t.Fatal(err)
	}
	target, err := app.GetNote(targetID)
	if err != nil {
		t.Fatal(err)
	}
	target.Content = "See " + attachment.URL
	if err := app.UpdateNote(targetID, Note{
		Title: target.Title, Content: target.Content, Folder: target.Folder,
		Tags: target.Tags, Version: target.Version,
	}); err != nil {
		t.Fatal(err)
	}
	target, err = app.GetNote(targetID)
	if err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	if err := app.ExportBackup(&archive); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]bool, len(reader.File))
	for _, file := range reader.File {
		entries[file.Name] = true
	}
	if !entries["manifest.json"] || !entries[backupAttachmentPath(targetID, attachmentStorageName)] {
		t.Fatalf("backup is missing expected entries: %#v", entries)
	}

	// Restoring into an existing library must replace everything, including
	// attachment files, without changing note or attachment IDs.
	staleID, err := app.CreateNote(Note{Title: "Stale"})
	if err != nil {
		t.Fatal(err)
	}
	staleAttachment, err := app.SaveAttachment(staleID, strings.NewReader("stale"), "stale.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, stalePath, err := app.GetAttachment(staleID, staleAttachment.ID)
	if err != nil {
		t.Fatal(err)
	}

	notes, attachments, err := app.RestoreBackup(archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if notes != 2 || attachments != 1 {
		t.Fatalf("unexpected restore counts: notes=%d attachments=%d", notes, attachments)
	}
	page, err := app.ListNotes("", "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("restore kept stale notes: %#v", page)
	}
	restored, err := app.GetNote(targetID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Content != target.Content || restored.Folder != "Notes" ||
		len(restored.Tags) != 2 || restored.Version != target.Version {
		t.Fatalf("restored note differs from the export: %#v", restored)
	}
	versions, err := app.ListNoteVersions(targetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("restore did not preserve the note history: %#v", versions)
	}
	if _, err := app.GetNote(staleID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale note survived the restore: %v", err)
	}
	if _, err := os.Stat(stalePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale attachment file survived the restore: %v", err)
	}
	restoredAttachment, restoredPath, err := app.GetAttachment(targetID, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restoredAttachment.Filename != "hello.txt" || restoredAttachment.Size != int64(len("attachment bytes")) {
		t.Fatalf("unexpected restored attachment: %#v", restoredAttachment)
	}
	content, err := os.ReadFile(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "attachment bytes" {
		t.Fatalf("restored attachment content = %q", content)
	}
	if _, err := os.Stat(app.attachmentsDir + ".previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore left the previous attachment directory behind: %v", err)
	}

	graph, err := app.NoteGraph()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range graph.Edges {
		if edge.Source == sourceID && edge.Target == targetID && edge.Type == "wikilink" {
			found = true
		}
	}
	if !found {
		t.Fatalf("restore did not rebuild note links: %#v", graph.Edges)
	}

	// A malformed archive must not touch the current library.
	if _, _, err := app.RestoreBackup([]byte("not a zip")); !errors.Is(err, errInvalidBackup) {
		t.Fatalf("malformed archive error = %v, want errInvalidBackup", err)
	}
	if _, err := app.GetNote(targetID); err != nil {
		t.Fatalf("malformed restore damaged the library: %v", err)
	}

	// The HTTP endpoint must serve a downloadable archive and accept it back.
	handler := app.Handler(filepath.Join("..", "web", "frontend"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/backup", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("backup download status=%d type=%q", response.Code, response.Header().Get("Content-Type"))
	}
	if _, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len())); err != nil {
		t.Fatalf("download is not a zip archive: %v", err)
	}

	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/backup", bytes.NewReader(archive.Bytes()))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("restore endpoint status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]int
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["notes"] != 2 || payload["attachments"] != 1 {
		t.Fatalf("unexpected restore response: %#v", payload)
	}
}

func TestBackupRejectsUnsafeAttachmentPaths(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err := app.CreateNote(Note{Title: "Keep"}); err != nil {
		t.Fatal(err)
	}

	manifest := backupManifest{
		Format: backupFormat, Version: backupVersion,
		Notes: []backupNote{{ID: 1, Title: "Evil", Version: 1}},
		Attachments: []backupAttachment{{
			ID: 1, NoteID: 1, Filename: "evil.txt", StorageName: "../escape",
			MediaType: "text/plain", Size: 1,
		}},
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(entry).Encode(manifest); err != nil {
		t.Fatal(err)
	}
	file, err := writer.Create("attachments/1/escape")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := app.RestoreBackup(archive.Bytes()); !errors.Is(err, errInvalidBackup) {
		t.Fatalf("unsafe storage name error = %v, want errInvalidBackup", err)
	}
	if page, err := app.ListNotes("", "", 10, 0); err != nil || page.Total != 1 ||
		page.Items[0].Title != "Keep" {
		t.Fatalf("unsafe restore damaged the library: %#v err=%v", page, err)
	}
}

func TestNoteHistorySnapshotsAndRestoresVersions(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	id, err := app.CreateNote(Note{Title: "History", Content: "first"})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"second", "third"} {
		note, err := app.GetNote(id)
		if err != nil {
			t.Fatal(err)
		}
		note.Content = content
		if err := app.UpdateNote(id, note); err != nil {
			t.Fatal(err)
		}
	}

	versions, err := app.ListNoteVersions(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected two snapshots, got %#v", versions)
	}
	if versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("snapshots are not newest first: %#v", versions)
	}
	first, err := app.GetNoteVersion(id, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != "first" || first.Title != "History" {
		t.Fatalf("unexpected snapshot content: %#v", first)
	}

	current, err := app.GetNote(id)
	if err != nil {
		t.Fatal(err)
	}
	_, staleErr := app.RestoreNoteVersion(id, versions[1].ID, current.Version-1)
	if !errors.Is(staleErr, ErrVersionConflict) {
		t.Fatalf("stale restore error = %v, want ErrVersionConflict", staleErr)
	}

	restored, err := app.RestoreNoteVersion(id, versions[1].ID, current.Version)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Content != "first" || restored.Version != current.Version+1 {
		t.Fatalf("unexpected restored note: %#v", restored)
	}
	versions, err = app.ListNoteVersions(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 || versions[0].Version != current.Version {
		t.Fatalf("restore did not snapshot the previous state: %#v", versions)
	}

	// Old snapshots are pruned so a note edited for a long time does not grow
	// the database without bound.
	for index := 0; index < noteVersionLimit+5; index++ {
		note, err := app.GetNote(id)
		if err != nil {
			t.Fatal(err)
		}
		note.Content = fmt.Sprintf("edit %d", index)
		if err := app.UpdateNote(id, note); err != nil {
			t.Fatal(err)
		}
	}
	versions, err = app.ListNoteVersions(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != noteVersionLimit {
		t.Fatalf("history kept %d versions, want %d", len(versions), noteVersionLimit)
	}

	handler := app.Handler(filepath.Join("..", "web", "frontend"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/notes/%d/versions", id), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("history endpoint status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload []NoteVersion
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != noteVersionLimit {
		t.Fatalf("history endpoint returned %d versions", len(payload))
	}

	response = httptest.NewRecorder()
	current, err = app.GetNote(id)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/notes/%d/versions/%d/restore", id, payload[0].ID),
		strings.NewReader(fmt.Sprintf(`{"version":%d}`, current.Version)))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("restore endpoint status = %d, body = %s", response.Code, response.Body.String())
	}
	var note Note
	if err := json.Unmarshal(response.Body.Bytes(), &note); err != nil {
		t.Fatal(err)
	}
	if note.ID != id || note.Content == "" {
		t.Fatalf("unexpected restored payload: %#v", note)
	}
}

func TestOptimisticConcurrencyRejectsStaleUpdates(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	id, err := app.CreateNote(Note{Title: "Concurrent", Content: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.GetNote(id)
	if err != nil || first.Version != 1 {
		t.Fatalf("initial version: %#v err=%v", first, err)
	}
	if err := app.UpdateNote(id, Note{
		Title: "Concurrent", Content: "v2", Version: first.Version,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := app.GetNote(id)
	if err != nil || second.Version != first.Version+1 || second.Content != "v2" {
		t.Fatalf("updated version: %#v err=%v", second, err)
	}

	err = app.UpdateNote(id, Note{
		Title: "Concurrent", Content: "stale", Version: first.Version,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale update error = %v, want ErrVersionConflict", err)
	}
	if current, err := app.GetNote(id); err != nil || current.Content != "v2" {
		t.Fatalf("stale update changed the note: %#v err=%v", current, err)
	}

	// Internal callers that do not provide a version keep the unconditional
	// behaviour used by migrations and tests.
	if err := app.UpdateNote(id, Note{Title: "Concurrent", Content: "forced"}); err != nil {
		t.Fatal(err)
	}

	handler := app.Handler(filepath.Join("..", "web", "frontend"))
	request := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/notes/%d", id),
		strings.NewReader(`{"title":"Concurrent","content":"conflict","version":1}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Note  Note   `json:"note"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error == "" || payload.Note.Content != "forced" || payload.Note.Version != 3 {
		t.Fatalf("unexpected conflict payload: %#v", payload)
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
		if version != 4 {
			app.Close()
			t.Fatalf("schema version = %d, want 4", version)
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
