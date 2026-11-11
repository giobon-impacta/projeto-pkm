package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLegacyMigrationSearchAndCRUD(t *testing.T) {
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
	_, err = db.Exec(`INSERT INTO notes(title, content, folder, tags)
		VALUES ('Orion archive', 'The orchid garden is hidden here', 'Legacy/Notes', 'old,kept')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	app, err := NewApp(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	titleResults, err := app.SearchNotes("ori", "title", "", "", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if titleResults.Total != 1 || titleResults.Items[0].Title != "Orion archive" {
		t.Fatalf("unexpected title search results: %#v", titleResults)
	}
	if len(titleResults.Items[0].Tags) != 2 || titleResults.Items[0].Tags[1] != "kept" {
		t.Fatalf("tags were not preserved: %#v", titleResults.Items[0].Tags)
	}

	contentResults, err := app.SearchNotes("orch", "content", "Legacy/Notes", "", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if contentResults.Total != 1 {
		t.Fatalf("expected one content match, got %d", contentResults.Total)
	}

	if err := app.UpdateNote(1, Note{
		Title:   "Orion updated",
		Content: "fresh river",
		Folder:  "Legacy/Ideas",
		Tags:    []string{"new"},
	}); err != nil {
		t.Fatal(err)
	}
	oldContent, err := app.SearchNotes("orchid", "content", "", "", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if oldContent.Total != 0 {
		t.Fatalf("old content remains searchable after update: %#v", oldContent)
	}
	newContent, err := app.SearchNotes("fresh", "content", "", "", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if newContent.Total != 1 {
		t.Fatalf("updated content was not indexed: %#v", newContent)
	}

	folders, err := app.FolderTree()
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].Path != "Legacy" ||
		len(folders[0].Children) != 1 || folders[0].Children[0].Path != "Legacy/Ideas" {
		t.Fatalf("unexpected virtual folder tree: %#v", folders)
	}
	page, err := app.ListNotes("Legacy/Ideas", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Title != "Orion updated" {
		t.Fatalf("folder filtering failed: %#v", page)
	}

	id, err := app.CreateNote(Note{Title: "New entry", Content: "searchable quartz", Folder: "Legacy/Ideas"})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteNote(id); err != nil {
		t.Fatal(err)
	}
	deleted, err := app.SearchNotes("quartz", "content", "", "", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Total != 0 {
		t.Fatalf("deleted content remains searchable: %#v", deleted)
	}
}

func TestNotesAPIAndPagination(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Handler(filepath.Join("..", "web", "frontend"))

	rootRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	rootResponse := httptest.NewRecorder()
	handler.ServeHTTP(rootResponse, rootRequest)
	if rootResponse.Code != http.StatusOK || !strings.Contains(rootResponse.Body.String(), "<title>PKM Notes</title>") {
		t.Fatalf("homepage response = %d, body = %s", rootResponse.Code, rootResponse.Body.String())
	}
	if cacheControl := rootResponse.Header().Get("Cache-Control"); cacheControl != "no-cache" {
		t.Fatalf("homepage Cache-Control = %q, want no-cache", cacheControl)
	}

	scriptRequest := httptest.NewRequest(http.MethodGet, "/src/main.js", nil)
	scriptResponse := httptest.NewRecorder()
	handler.ServeHTTP(scriptResponse, scriptRequest)
	if scriptResponse.Code != http.StatusOK {
		t.Fatalf("script response = %d, body = %s", scriptResponse.Code, scriptResponse.Body.String())
	}
	if cacheControl := scriptResponse.Header().Get("Cache-Control"); cacheControl != "no-cache" {
		t.Fatalf("script Cache-Control = %q, want no-cache", cacheControl)
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/notes", strings.NewReader(
		`{"title":"API note","content":"body","folder":"Work","tags":["api"]}`,
	))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var note Note
	if err := json.Unmarshal(createResponse.Body.Bytes(), &note); err != nil {
		t.Fatal(err)
	}
	if note.ID < 1 || note.Title != "API note" || len(note.Tags) != 1 {
		t.Fatalf("unexpected created note: %#v", note)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/notes?limit=1&offset=0", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var page Page
	if err := json.Unmarshal(listResponse.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Limit != 1 || len(page.Items) != 1 {
		t.Fatalf("unexpected paginated response: %#v", page)
	}

	notePath := "/api/notes/" + strconv.FormatInt(note.ID, 10)
	getRequest := httptest.NewRequest(http.MethodGet, notePath, nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}

	updateRequest := httptest.NewRequest(http.MethodPut, notePath,
		strings.NewReader(`{"title":"Updated API note","content":"new indexed body","folder":"Work/Ideas"}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}

	searchRequest := httptest.NewRequest(http.MethodGet, "/api/search?q=indexed&mode=content", nil)
	searchResponse := httptest.NewRecorder()
	handler.ServeHTTP(searchResponse, searchRequest)
	if searchResponse.Code != http.StatusOK || !strings.Contains(searchResponse.Body.String(), "Updated API note") {
		t.Fatalf("content search response = %d, body = %s", searchResponse.Code, searchResponse.Body.String())
	}
	var contentPage Page
	if err := json.Unmarshal(searchResponse.Body.Bytes(), &contentPage); err != nil {
		t.Fatal(err)
	}
	if len(contentPage.Items) != 1 || !strings.Contains(contentPage.Items[0].Preview, "<mark>indexed</mark>") {
		t.Fatalf("content search preview does not highlight the match: %#v", contentPage.Items)
	}

	folderRequest := httptest.NewRequest(http.MethodGet, "/api/folders", nil)
	folderResponse := httptest.NewRecorder()
	handler.ServeHTTP(folderResponse, folderRequest)
	if folderResponse.Code != http.StatusOK || !strings.Contains(folderResponse.Body.String(), "Work/Ideas") {
		t.Fatalf("folder response = %d, body = %s", folderResponse.Code, folderResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, notePath, nil)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}

	invalidSearch := httptest.NewRequest(http.MethodGet, "/api/search?q=words", nil)
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidSearch)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("search without mode status = %d", invalidResponse.Code)
	}
}

func TestLargeCollectionSearchAndPagination(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	tx, err := app.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		content := "ordinary searchable note"
		if i == 9999 {
			content = "needle match in a large collection"
		}
		if _, err := tx.Exec("INSERT INTO notes(title, content) VALUES (?, ?)",
			"Note "+strconv.Itoa(i), content); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	page, err := app.ListNotes("", "", 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 10000 || len(page.Items) != 25 || page.Limit != 25 {
		t.Fatalf("large collection was not paginated: total=%d items=%d limit=%d", page.Total, len(page.Items), page.Limit)
	}

	results, err := app.SearchNotes("needle", "content", "", "", 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if results.Total != 1 || len(results.Items) != 1 || results.Items[0].Title != "Note 9999" {
		t.Fatalf("FTS did not isolate the large collection match: %#v", results)
	}
}

func TestFolderValidation(t *testing.T) {
	note := Note{Title: "safe title", Folder: "../outside"}
	if err := validateNote(&note); err == nil {
		t.Fatal("expected traversal-like folder path to be rejected")
	}
	note = Note{Title: "safe title", Folder: `Work\Ideas`}
	if err := validateNote(&note); err != nil {
		t.Fatal(err)
	}
	if note.Folder != "Work/Ideas" {
		t.Fatalf("folder was not normalized: %q", note.Folder)
	}
}

func TestFolderNotesEndpointListsExactFolderAndRoot(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Handler(filepath.Join("..", "web", "frontend"))
	for _, note := range []Note{
		{Title: "Root note", Content: "root"},
		{Title: "Project note", Content: "project", Folder: "Projects"},
		{Title: "Nested note", Content: "nested", Folder: "Projects/Ideas"},
	} {
		if _, err := app.CreateNote(note); err != nil {
			t.Fatal(err)
		}
	}

	for _, testCase := range []struct {
		name      string
		url       string
		wantTitle string
		wantTotal int
		unwanted  string
	}{
		{name: "root", url: "/api/folder-notes?folder=&limit=10", wantTitle: "Root note", wantTotal: 1, unwanted: "Project note"},
		{name: "exact folder", url: "/api/folder-notes?folder=Projects&limit=10", wantTitle: "Project note", wantTotal: 1, unwanted: "Nested note"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.url, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var page Page
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Total != testCase.wantTotal || len(page.Items) != 1 || page.Items[0].Title != testCase.wantTitle {
				t.Fatalf("unexpected folder page: %#v", page)
			}
			if strings.Contains(response.Body.String(), testCase.unwanted) {
				t.Fatalf("folder listing included a note outside the exact folder: %s", response.Body.String())
			}
		})
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/folder-notes?folder=..%2Foutside", nil)
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid folder status = %d", invalidResponse.Code)
	}
}

func TestFolderAndNoteMoves(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	rootID, err := app.CreateNote(Note{Title: "Project overview", Folder: "Projects"})
	if err != nil {
		t.Fatal(err)
	}
	nestedID, err := app.CreateNote(Note{Title: "Idea", Folder: "Projects/Ideas"})
	if err != nil {
		t.Fatal(err)
	}
	prefixID, err := app.CreateNote(Note{Title: "Not a child", Folder: "Projects-old"})
	if err != nil {
		t.Fatal(err)
	}

	handler := app.Handler(filepath.Join("..", "web", "frontend"))
	moveFolder := httptest.NewRequest(http.MethodPost, "/api/folders/move",
		strings.NewReader(`{"source":"Projects","target":"Archive"}`))
	moveFolder.Header.Set("Content-Type", "application/json")
	moveFolderResponse := httptest.NewRecorder()
	handler.ServeHTTP(moveFolderResponse, moveFolder)
	if moveFolderResponse.Code != http.StatusOK || !strings.Contains(moveFolderResponse.Body.String(), `"folder":"Archive/Projects"`) {
		t.Fatalf("folder move response = %d, body = %s", moveFolderResponse.Code, moveFolderResponse.Body.String())
	}
	root, err := app.GetNote(rootID)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := app.GetNote(nestedID)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := app.GetNote(prefixID)
	if err != nil {
		t.Fatal(err)
	}
	if root.Folder != "Archive/Projects" || nested.Folder != "Archive/Projects/Ideas" || prefix.Folder != "Projects-old" {
		t.Fatalf("folder move changed incorrect paths: root=%q nested=%q prefix=%q", root.Folder, nested.Folder, prefix.Folder)
	}

	cycle := httptest.NewRequest(http.MethodPost, "/api/folders/move",
		strings.NewReader(`{"source":"Archive/Projects","target":"Archive/Projects/Ideas"}`))
	cycle.Header.Set("Content-Type", "application/json")
	cycleResponse := httptest.NewRecorder()
	handler.ServeHTTP(cycleResponse, cycle)
	if cycleResponse.Code != http.StatusBadRequest {
		t.Fatalf("moving folder into descendant status = %d", cycleResponse.Code)
	}

	moveNote := httptest.NewRequest(http.MethodPatch, "/api/notes/"+strconv.FormatInt(rootID, 10)+"/folder",
		strings.NewReader(`{"folder":""}`))
	moveNote.Header.Set("Content-Type", "application/json")
	moveNoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(moveNoteResponse, moveNote)
	if moveNoteResponse.Code != http.StatusOK {
		t.Fatalf("note move status = %d, body = %s", moveNoteResponse.Code, moveNoteResponse.Body.String())
	}
	root, err = app.GetNote(rootID)
	if err != nil || root.Folder != "" {
		t.Fatalf("note was not moved to root: note=%#v err=%v", root, err)
	}
}

func TestFrontmatterTagsAreStoredAsNoteMetadata(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
		found   bool
	}{
		{name: "scalar", content: "---\ntags: sei-la\n---\n\nBody", want: []string{"sei-la"}, found: true},
		{name: "inline list", content: "---\ntags: [pkm, notas]\n---\nBody", want: []string{"pkm", "notas"}, found: true},
		{name: "block list", content: "---\ntags:\n  - pkm\n  - notas\n---\nBody", want: []string{"pkm", "notas"}, found: true},
		{name: "absent", content: "# Body", want: nil, found: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, found := frontmatterTags(testCase.content)
			if found != testCase.found || strings.Join(got, ",") != strings.Join(testCase.want, ",") {
				t.Fatalf("frontmatterTags() = %v, %v; want %v, %v", got, found, testCase.want, testCase.found)
			}
		})
	}

	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	id, err := app.CreateNote(Note{Title: "Frontmatter", Content: "---\ntags: sei-la\n---\n\nBody"})
	if err != nil {
		t.Fatal(err)
	}
	note, err := app.GetNote(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(note.Tags) != 1 || note.Tags[0] != "sei-la" {
		t.Fatalf("frontmatter tags not returned as metadata: %#v", note.Tags)
	}
}

func TestMarkdownPreviewRestrictsUnsafeContent(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/preview",
		strings.NewReader("# Heading\n\n<script>alert(1)</script>\n\n[unsafe](javascript:alert(1))"))
	response := httptest.NewRecorder()
	app.handlePreview(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", response.Code, response.Body.String())
	}
	rendered := response.Body.String()
	if !strings.Contains(rendered, "<h1>Heading</h1>") {
		t.Fatalf("markdown heading was not rendered: %s", rendered)
	}
	if strings.Contains(rendered, "<script>") || strings.Contains(rendered, `href="javascript:`) {
		t.Fatalf("unsafe markdown content was rendered: %s", rendered)
	}
	if !strings.Contains(rendered, `href="#"`) {
		t.Fatalf("unsafe link should be neutralized: %s", rendered)
	}
}

func TestMarkdownPreviewRendersGFMExtensions(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	source := "| A | B |\n| --- | --- |\n| 1 | 2 |\n\n" +
		"~~riscado~~\n\n- [ ] pendente\n- [x] feita\n\nhttps://example.com\n"
	request := httptest.NewRequest(http.MethodPost, "/api/preview", strings.NewReader(source))
	response := httptest.NewRecorder()
	app.handlePreview(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", response.Code, response.Body.String())
	}
	rendered := response.Body.String()
	for _, expected := range []string{
		"<table>",
		"<del>riscado</del>",
		`type="checkbox"`,
		`href="https://example.com"`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("GFM output is missing %q: %s", expected, rendered)
		}
	}
}
