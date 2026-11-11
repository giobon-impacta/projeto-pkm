package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var wikilinkPattern = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]*)?\]\]`)
var wikilinkPreviewPattern = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]*))?\]\]`)
var inlineCodePattern = regexp.MustCompile("`+[^`\\n]*`+")

type GraphNode struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Folder string `json:"folder"`
}

type GraphEdge struct {
	Source int64  `json:"source"`
	Target int64  `json:"target"`
	Type   string `json:"type"`
}

type NoteGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type indexedNote struct {
	GraphNode
}

type linkTarget struct {
	value string
	kind  string
}

func convertWikilinksToMarkdown(content string, source indexedNote, byPath, byTitle map[string]int64) string {
	lines := strings.Split(content, "\n")
	var result strings.Builder
	var fenceChar byte
	fenceLength := 0
	for index, line := range lines {
		if index > 0 {
			result.WriteByte('\n')
		}
		trimmed := strings.TrimSpace(line)
		if marker := markdownFence(trimmed); marker != "" {
			if fenceChar == 0 {
				fenceChar = marker[0]
				fenceLength = len(marker)
			} else if marker[0] == fenceChar && len(marker) >= fenceLength {
				fenceChar = 0
				fenceLength = 0
			}
			result.WriteString(line)
			continue
		}
		if fenceChar != 0 {
			result.WriteString(line)
			continue
		}
		matches := inlineCodePattern.FindAllStringIndex(line, -1)
		cursor := 0
		for _, match := range matches {
			result.WriteString(convertWikilinkText(line[cursor:match[0]], source, byPath, byTitle))
			result.WriteString(line[match[0]:match[1]])
			cursor = match[1]
		}
		result.WriteString(convertWikilinkText(line[cursor:], source, byPath, byTitle))
	}
	return result.String()
}

func convertWikilinkText(value string, source indexedNote, byPath, byTitle map[string]int64) string {
	matches := wikilinkPreviewPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	var result strings.Builder
	cursor := 0
	for _, match := range matches {
		if match[0] > 0 && value[match[0]-1] == '!' {
			continue
		}
		target := strings.TrimSpace(value[match[2]:match[3]])
		noteID := resolveNoteLink(source, target, byPath, byTitle)
		if noteID == 0 {
			continue
		}
		label := target
		if match[4] >= 0 {
			if alias := strings.TrimSpace(value[match[4]:match[5]]); alias != "" {
				label = alias
			}
		}
		result.WriteString(value[cursor:match[0]])
		result.WriteByte('[')
		result.WriteString(escapeMarkdownLinkLabel(label))
		result.WriteString("](?note=")
		result.WriteString(strconv.FormatInt(noteID, 10))
		result.WriteByte(')')
		cursor = match[1]
	}
	if cursor == 0 {
		return value
	}
	result.WriteString(value[cursor:])
	return result.String()
}

func escapeMarkdownLinkLabel(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\", "[", "\\[", "]", "\\]",
		"*", "\\*", "_", "\\_", "`", "\\`",
	)
	return replacer.Replace(value)
}

func (a *App) NoteGraph() (NoteGraph, error) {
	notes, err := a.readNotesForLinks()
	if err != nil {
		return NoteGraph{}, err
	}
	byPath, byTitle := makeNoteLinkIndexes(notes)
	graph := NoteGraph{
		Nodes: make([]GraphNode, 0, len(notes)),
		Edges: make([]GraphEdge, 0),
	}
	notesByID := make(map[int64]indexedNote, len(notes))
	for _, note := range notes {
		notesByID[note.ID] = note
		graph.Nodes = append(graph.Nodes, note.GraphNode)
	}
	seenEdges := make(map[[3]int64]struct{})
	rows, err := a.db.Query(`SELECT source_id, target_key, kind
		FROM note_links ORDER BY source_id, target_key, kind`)
	if err != nil {
		return NoteGraph{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID int64
		var targetKey, kind string
		if err := rows.Scan(&sourceID, &targetKey, &kind); err != nil {
			return NoteGraph{}, err
		}
		note, exists := notesByID[sourceID]
		if !exists {
			continue
		}
		targetID := resolveNoteLink(note, targetKey, byPath, byTitle)
		if targetID == 0 || targetID == note.ID {
			continue
		}
		edgeKey := [3]int64{note.ID, targetID, int64(linkKindID(kind))}
		if _, found := seenEdges[edgeKey]; found {
			continue
		}
		seenEdges[edgeKey] = struct{}{}
		graph.Edges = append(graph.Edges, GraphEdge{Source: note.ID, Target: targetID, Type: kind})
	}
	if err := rows.Err(); err != nil {
		return NoteGraph{}, err
	}
	sort.Slice(graph.Nodes, func(i, j int) bool { return graph.Nodes[i].ID < graph.Nodes[j].ID })
	sort.Slice(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].Source != graph.Edges[j].Source {
			return graph.Edges[i].Source < graph.Edges[j].Source
		}
		if graph.Edges[i].Target != graph.Edges[j].Target {
			return graph.Edges[i].Target < graph.Edges[j].Target
		}
		return graph.Edges[i].Type < graph.Edges[j].Type
	})
	return graph, nil
}

func (a *App) Backlinks(noteID int64) ([]NoteSummary, error) {
	var exists int
	if err := a.db.QueryRow("SELECT 1 FROM notes WHERE rowid = ?", noteID).Scan(&exists); err != nil {
		return nil, err
	}
	graph, err := a.NoteGraph()
	if err != nil {
		return nil, err
	}
	backlinkIDs := make([]int64, 0)
	seenIDs := make(map[int64]struct{})
	for _, edge := range graph.Edges {
		if edge.Target != noteID {
			continue
		}
		if _, found := seenIDs[edge.Source]; found {
			continue
		}
		seenIDs[edge.Source] = struct{}{}
		backlinkIDs = append(backlinkIDs, edge.Source)
	}
	if len(backlinkIDs) == 0 {
		return []NoteSummary{}, nil
	}

	// SQLite allows at most 999 bound parameters by default. Fetch in chunks
	// instead of loading every note summary just to filter it in Go.
	const chunkSize = 500
	result := make([]NoteSummary, 0, len(backlinkIDs))
	for start := 0; start < len(backlinkIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(backlinkIDs) {
			end = len(backlinkIDs)
		}
		chunk := backlinkIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		arguments := make([]any, len(chunk))
		for index, id := range chunk {
			arguments[index] = id
		}
		rows, err := a.db.Query(`SELECT rowid, title, folder, substr(COALESCE(content, ''), 1, 180),
			updated_at, COALESCE(tags, '') FROM notes WHERE rowid IN (`+placeholders+`)`,
			arguments...)
		if err != nil {
			return nil, err
		}
		items, err := scanSummaries(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := strings.ToLower(result[i].Title), strings.ToLower(result[j].Title)
		if left != right {
			return left < right
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (a *App) readNotesForLinks() ([]indexedNote, error) {
	rows, err := a.db.Query(`SELECT rowid, title, COALESCE(folder, '') FROM notes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]indexedNote, 0)
	for rows.Next() {
		var note indexedNote
		if err := rows.Scan(&note.ID, &note.Title, &note.Folder); err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, rows.Err()
}

func makeNoteLinkIndexes(notes []indexedNote) (map[string]int64, map[string]int64) {
	byPath := make(map[string]int64, len(notes)*2)
	byTitle := make(map[string]int64, len(notes))
	for _, note := range notes {
		title := strings.TrimSpace(note.Title)
		titleKey := normalizeNoteLinkKey(title)
		if _, exists := byTitle[titleKey]; !exists {
			byTitle[titleKey] = note.ID
		}
		fullPath := path.Join(note.Folder, title)
		byPath[normalizeNoteLinkKey(fullPath)] = note.ID
		byPath[normalizeNoteLinkKey(fullPath+".md")] = note.ID
	}
	return byPath, byTitle
}

func extractNoteLinks(content string) ([]linkTarget, error) {
	source := []byte(content)
	document := goldmark.New().Parser().Parse(text.NewReader(source))
	targets := make([]linkTarget, 0)
	if err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if link, ok := node.(*ast.Link); ok {
				targets = append(targets, linkTarget{value: string(link.Destination), kind: "markdown"})
			}
		}
		return ast.WalkContinue, nil
	}); err != nil {
		return nil, err
	}
	plain := stripCodeForWikilinks(content)
	for _, match := range wikilinkPattern.FindAllStringSubmatch(plain, -1) {
		targets = append(targets, linkTarget{value: strings.TrimSpace(match[1]), kind: "wikilink"})
	}
	return targets, nil
}

const maxStoredLinkTarget = 2048

// replaceNoteLinks keeps the persisted raw link targets in sync with the note
// content. Only the edited note is parsed; target resolution is deferred to
// NoteGraph and Backlinks so renames stay cheap.
func replaceNoteLinks(tx *sql.Tx, sourceID int64, content string) error {
	if _, err := tx.Exec("DELETE FROM note_links WHERE source_id = ?", sourceID); err != nil {
		return err
	}
	targets, err := extractNoteLinks(content)
	if err != nil {
		return err
	}
	seen := make(map[[2]string]struct{}, len(targets))
	for _, target := range targets {
		key := strings.TrimSpace(target.value)
		if key == "" || len(key) > maxStoredLinkTarget {
			continue
		}
		pair := [2]string{key, target.kind}
		if _, found := seen[pair]; found {
			continue
		}
		seen[pair] = struct{}{}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO note_links(source_id, target_key, kind)
			VALUES (?, ?, ?)`, sourceID, key, target.kind); err != nil {
			return err
		}
	}
	return nil
}

func stripCodeForWikilinks(content string) string {
	lines := strings.Split(content, "\n")
	var result strings.Builder
	var fenceChar byte
	fenceLength := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if marker := markdownFence(trimmed); marker != "" {
			if fenceChar == 0 {
				fenceChar = marker[0]
				fenceLength = len(marker)
			} else if marker[0] == fenceChar && len(marker) >= fenceLength {
				fenceChar = 0
				fenceLength = 0
			}
			result.WriteByte('\n')
			continue
		}
		if fenceChar != 0 {
			result.WriteByte('\n')
			continue
		}
		result.WriteString(inlineCodePattern.ReplaceAllString(line, ""))
		result.WriteByte('\n')
	}
	return result.String()
}

func markdownFence(line string) string {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return ""
	}
	count := 0
	for count < len(line) && line[count] == line[0] {
		count++
	}
	if count < 3 {
		return ""
	}
	return line[:count]
}

func resolveNoteLink(source indexedNote, rawTarget string, byPath, byTitle map[string]int64) int64 {
	target := strings.TrimSpace(rawTarget)
	if target == "" || strings.HasPrefix(target, "#") {
		return 0
	}
	if strings.HasPrefix(target, "//") {
		return 0
	}
	// Most targets are plain titles or relative paths. Parsing every one as a
	// URL is unnecessary and shows up in graph benchmarks; only do it when the
	// target actually contains URL syntax.
	if strings.ContainsAny(target, "?#%:") {
		parsed, err := url.Parse(target)
		if err != nil || parsed.Scheme != "" || parsed.Host != "" {
			return 0
		}
		target = parsed.Path
		if decoded, err := url.PathUnescape(target); err == nil {
			target = decoded
		}
	}
	if target == "" {
		return 0
	}
	target = strings.TrimPrefix(target, "/")
	if strings.HasSuffix(strings.ToLower(target), ".md") {
		target = target[:len(target)-3]
	}
	if target == "" || target == "." {
		return 0
	}
	candidates := []string{target}
	if !strings.HasPrefix(rawTarget, "/") && source.Folder != "" {
		relative := path.Clean(path.Join(source.Folder, target))
		if relative != ".." && !strings.HasPrefix(relative, "../") {
			candidates = append([]string{relative}, candidates...)
		}
	}
	for _, candidate := range candidates {
		if id := byPath[normalizeNoteLinkKey(candidate)]; id != 0 {
			return id
		}
		if id := byPath[normalizeNoteLinkKey(candidate+".md")]; id != 0 {
			return id
		}
	}
	return byTitle[normalizeNoteLinkKey(path.Base(target))]
}

func normalizeNoteLinkKey(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	value = strings.TrimSuffix(value, ".md")
	return strings.ToLower(path.Clean(value))
}

func linkKindID(kind string) int {
	if kind == "wikilink" {
		return 1
	}
	return 0
}

func (a *App) handleBacklinks(w http.ResponseWriter, r *http.Request, noteID int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	items, err := a.Backlinks(noteID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
	} else if err != nil {
		log.Printf("load backlinks for note %d: %v", noteID, err)
		http.Error(w, "failed to load backlinks", http.StatusInternalServerError)
	} else {
		writeJSON(w, http.StatusOK, items)
	}
}

func (a *App) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	graph, err := a.NoteGraph()
	if err != nil {
		log.Printf("build note graph: %v", err)
		http.Error(w, "failed to build note graph", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, graph)
}
