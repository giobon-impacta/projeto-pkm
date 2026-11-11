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
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var wikilinkPattern = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]*)?\]\]`)
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
	rows, err := a.db.Query("SELECT rowid, COALESCE(content, '') FROM notes")
	if err != nil {
		return NoteGraph{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var noteID int64
		var content string
		if err := rows.Scan(&noteID, &content); err != nil {
			return NoteGraph{}, err
		}
		note, exists := notesByID[noteID]
		if !exists {
			continue
		}
		targets, err := extractNoteLinks(content)
		if err != nil {
			return NoteGraph{}, err
		}
		for _, target := range targets {
			targetID := resolveNoteLink(note, target.value, byPath, byTitle)
			if targetID == 0 || targetID == note.ID {
				continue
			}
			edgeKey := [3]int64{note.ID, targetID, int64(linkKindID(target.kind))}
			if _, found := seenEdges[edgeKey]; found {
				continue
			}
			seenEdges[edgeKey] = struct{}{}
			graph.Edges = append(graph.Edges, GraphEdge{Source: note.ID, Target: targetID, Type: target.kind})
		}
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
	graph, err := a.NoteGraph()
	if err != nil {
		return nil, err
	}
	known := false
	for _, node := range graph.Nodes {
		if node.ID == noteID {
			known = true
			break
		}
	}
	if !known {
		return nil, sql.ErrNoRows
	}
	backlinkIDs := make(map[int64]struct{})
	for _, edge := range graph.Edges {
		if edge.Target == noteID {
			backlinkIDs[edge.Source] = struct{}{}
		}
	}
	if len(backlinkIDs) == 0 {
		return []NoteSummary{}, nil
	}
	rows, err := a.db.Query(`SELECT rowid, title, folder, substr(COALESCE(content, ''), 1, 180), updated_at, COALESCE(tags, '')
		FROM notes ORDER BY title COLLATE NOCASE, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries, err := scanSummaries(rows)
	if err != nil {
		return nil, err
	}
	result := make([]NoteSummary, 0, len(backlinkIDs))
	for _, item := range summaries {
		if _, found := backlinkIDs[item.ID]; found {
			result = append(result, item)
		}
	}
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
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return 0
	}
	target = parsed.Path
	if target == "" {
		return 0
	}
	if decoded, err := url.PathUnescape(target); err == nil {
		target = decoded
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
