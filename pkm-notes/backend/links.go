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
var markdownInlineLinkPattern = regexp.MustCompile(`(!?\[[^\]\n]*\]\()(<[^>\n]*>|(?:[^()\s]|\([^()\s]*\))+)((?:\s+(?:"[^"]*"|'[^']*'))?\))`)
var markdownLinkDefinitionPattern = regexp.MustCompile(`^(\s{0,3}\[[^\]\n]+\]:\s*)(<[^>\n]*>|\S+)(.*)$`)

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

type noteQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func (a *App) readNotesForLinks() ([]indexedNote, error) {
	return readNotesForLinksWith(a.db)
}

func readNotesForLinksWith(queryer noteQueryer) ([]indexedNote, error) {
	rows, err := queryer.Query(`SELECT rowid, title, COALESCE(folder, '') FROM notes ORDER BY rowid`)
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

// rewriteIncomingLinks updates the source notes that referenced the renamed
// note before the update. Titles stored in note_links are raw, so renaming a
// note would otherwise leave every title-based link dangling.
func rewriteIncomingLinks(tx *sql.Tx, noteID int64, newTitle string, byPath, byTitle map[string]int64) error {
	rows, err := tx.Query(`SELECT l.source_id, l.target_key, n.title, COALESCE(n.folder, '')
		FROM note_links l JOIN notes n ON n.rowid = l.source_id
		WHERE l.source_id != ?`, noteID)
	if err != nil {
		return err
	}
	sources := make(map[int64]indexedNote)
	for rows.Next() {
		var sourceID int64
		var targetKey, title, folder string
		if err := rows.Scan(&sourceID, &targetKey, &title, &folder); err != nil {
			rows.Close()
			return err
		}
		source := indexedNote{GraphNode: GraphNode{ID: sourceID, Title: title, Folder: folder}}
		if resolveNoteLink(source, targetKey, byPath, byTitle) == noteID {
			sources[sourceID] = source
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	ids := make([]int64, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		var content string
		if err := tx.QueryRow("SELECT COALESCE(content, '') FROM notes WHERE rowid = ?", id).
			Scan(&content); err != nil {
			return err
		}
		rewritten, changed := rewriteLinksForRename(content, sources[id], noteID, newTitle, byPath, byTitle)
		if !changed {
			continue
		}
		if _, err := tx.Exec(`UPDATE notes SET content = ?, updated_at = datetime('now')
			WHERE rowid = ?`, rewritten, id); err != nil {
			return err
		}
		if err := replaceNoteLinks(tx, id, rewritten); err != nil {
			return err
		}
	}
	return nil
}

func rewriteLinksForRename(content string, source indexedNote, noteID int64, newTitle string, byPath, byTitle map[string]int64) (string, bool) {
	return rewriteContentOutsideCode(content,
		func(line string) (string, bool) {
			return rewriteLinkDefinitionLine(line, source, noteID, newTitle, byPath, byTitle)
		},
		func(segment string) (string, bool) {
			return rewriteLinksInText(segment, source, noteID, newTitle, byPath, byTitle)
		})
}

type linkTextRewriter func(string) (string, bool)

// rewriteContentOutsideCode applies rewriteLine to each line outside fenced
// code blocks and rewriteSegment to the parts of that line that are not inline
// code. It keeps the original line endings and code untouched.
func rewriteContentOutsideCode(content string, rewriteLine, rewriteSegment linkTextRewriter) (string, bool) {
	lines := strings.Split(content, "\n")
	var result strings.Builder
	var fenceChar byte
	fenceLength := 0
	changed := false
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
		if rewriteLine != nil {
			rewrittenLine, lineChanged := rewriteLine(line)
			line = rewrittenLine
			changed = changed || lineChanged
		}
		matches := inlineCodePattern.FindAllStringIndex(line, -1)
		cursor := 0
		for _, match := range matches {
			if rewriteSegment != nil {
				rewritten, segmentChanged := rewriteSegment(line[cursor:match[0]])
				result.WriteString(rewritten)
				changed = changed || segmentChanged
			} else {
				result.WriteString(line[cursor:match[0]])
			}
			result.WriteString(line[match[0]:match[1]])
			cursor = match[1]
		}
		if rewriteSegment != nil {
			rewritten, segmentChanged := rewriteSegment(line[cursor:])
			result.WriteString(rewritten)
			changed = changed || segmentChanged
		} else {
			result.WriteString(line[cursor:])
		}
	}
	if !changed {
		return content, false
	}
	return result.String(), true
}

func rewriteLinksInText(value string, source indexedNote, noteID int64, newTitle string, byPath, byTitle map[string]int64) (string, bool) {
	value, wikilinkChanged := rewriteWikilinksInText(value, source, noteID, newTitle, byPath, byTitle)
	value, markdownChanged := rewriteMarkdownLinksInText(value, source, noteID, newTitle, byPath, byTitle)
	return value, wikilinkChanged || markdownChanged
}

func rewriteWikilinksInText(value string, source indexedNote, noteID int64, newTitle string, byPath, byTitle map[string]int64) (string, bool) {
	matches := wikilinkPreviewPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, false
	}
	var result strings.Builder
	cursor := 0
	changed := false
	for _, match := range matches {
		rawTarget := value[match[2]:match[3]]
		if resolveNoteLink(source, strings.TrimSpace(rawTarget), byPath, byTitle) != noteID {
			continue
		}
		replacement := renameLinkTarget(rawTarget, newTitle, false)
		if replacement == rawTarget {
			continue
		}
		result.WriteString(value[cursor:match[2]])
		result.WriteString(replacement)
		cursor = match[3]
		changed = true
	}
	if !changed {
		return value, false
	}
	result.WriteString(value[cursor:])
	return result.String(), true
}

func rewriteMarkdownLinksInText(value string, source indexedNote, noteID int64, newTitle string, byPath, byTitle map[string]int64) (string, bool) {
	matches := markdownInlineLinkPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, false
	}
	var result strings.Builder
	cursor := 0
	changed := false
	for _, match := range matches {
		rawDestination := value[match[4]:match[5]]
		destination := rawDestination
		angleBracketed := strings.HasPrefix(destination, "<") && strings.HasSuffix(destination, ">")
		if angleBracketed {
			destination = destination[1 : len(destination)-1]
		}
		if resolveNoteLink(source, destination, byPath, byTitle) != noteID {
			continue
		}
		replacement := renameLinkTarget(destination, newTitle, !angleBracketed)
		if angleBracketed {
			replacement = "<" + replacement + ">"
		}
		if replacement == rawDestination {
			continue
		}
		result.WriteString(value[cursor:match[4]])
		result.WriteString(replacement)
		cursor = match[5]
		changed = true
	}
	if !changed {
		return value, false
	}
	result.WriteString(value[cursor:])
	return result.String(), true
}

func rewriteLinkDefinitionLine(line string, source indexedNote, noteID int64, newTitle string, byPath, byTitle map[string]int64) (string, bool) {
	match := markdownLinkDefinitionPattern.FindStringSubmatchIndex(line)
	if match == nil {
		return line, false
	}
	rawDestination := line[match[4]:match[5]]
	destination := rawDestination
	angleBracketed := strings.HasPrefix(destination, "<") && strings.HasSuffix(destination, ">")
	if angleBracketed {
		destination = destination[1 : len(destination)-1]
	}
	if resolveNoteLink(source, destination, byPath, byTitle) != noteID {
		return line, false
	}
	replacement := renameLinkTarget(destination, newTitle, !angleBracketed)
	if angleBracketed {
		replacement = "<" + replacement + ">"
	}
	if replacement == rawDestination {
		return line, false
	}
	return line[:match[4]] + replacement + line[match[5]:], true
}

// renameLinkTarget keeps the shape of the original target: surrounding
// whitespace, folder prefix, .md extension, fragment and query string.
func renameLinkTarget(raw, newTitle string, escape bool) string {
	leading := raw[:len(raw)-len(strings.TrimLeft(raw, " "))]
	trailing := raw[len(strings.TrimRight(raw, " ")):]
	target := strings.TrimSpace(raw)

	suffix := ""
	if index := strings.IndexAny(target, "?#"); index >= 0 {
		suffix = target[index:]
		target = target[:index]
	}
	extension := ""
	if strings.HasSuffix(strings.ToLower(target), ".md") {
		extension = target[len(target)-3:]
		target = target[:len(target)-3]
	}
	directory := ""
	if slash := strings.LastIndex(target, "/"); slash >= 0 {
		directory = target[:slash+1]
	}
	segment := newTitle
	if escape {
		segment = escapeLinkDestination(segment)
	}
	return leading + directory + segment + extension + suffix + trailing
}

func escapeLinkDestination(value string) string {
	if strings.ContainsAny(value, " \t()<>#?%") {
		return url.PathEscape(value)
	}
	return value
}

// relinkNotesAfterMove keeps the meaning of path-based links across folder
// moves. It compares the resolution before and after the move and rewrites
// only the targets that would otherwise resolve to another note or nothing.
func relinkNotesAfterMove(tx *sql.Tx, before, after []indexedNote) error {
	oldByPath, oldByTitle := makeNoteLinkIndexes(before)
	newByPath, newByTitle := makeNoteLinkIndexes(after)
	oldNotes := make(map[int64]indexedNote, len(before))
	for _, note := range before {
		oldNotes[note.ID] = note
	}
	newNotes := make(map[int64]indexedNote, len(after))
	for _, note := range after {
		newNotes[note.ID] = note
	}

	rows, err := tx.Query(`SELECT source_id, target_key FROM note_links ORDER BY source_id, target_key`)
	if err != nil {
		return err
	}
	affected := make(map[int64]struct{})
	for rows.Next() {
		var sourceID int64
		var targetKey string
		if err := rows.Scan(&sourceID, &targetKey); err != nil {
			rows.Close()
			return err
		}
		oldSource, exists := oldNotes[sourceID]
		if !exists {
			continue
		}
		oldTargetID := resolveNoteLink(oldSource, targetKey, oldByPath, oldByTitle)
		if oldTargetID == 0 {
			continue
		}
		newSource, exists := newNotes[sourceID]
		if !exists {
			continue
		}
		newTarget, exists := newNotes[oldTargetID]
		if !exists {
			continue
		}
		if pathStyleTarget(targetKey) {
			if oldSource.Folder != newSource.Folder || oldNotes[oldTargetID].Folder != newTarget.Folder {
				affected[sourceID] = struct{}{}
			}
			continue
		}
		if resolveNoteLink(newSource, targetKey, newByPath, newByTitle) == oldTargetID {
			continue
		}
		affected[sourceID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	ids := make([]int64, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		var content string
		if err := tx.QueryRow("SELECT COALESCE(content, '') FROM notes WHERE rowid = ?", id).
			Scan(&content); err != nil {
			return err
		}
		rewritten, changed := rewriteLinksForMove(content, oldNotes[id], newNotes[id],
			oldByPath, oldByTitle, newByPath, newByTitle, oldNotes, newNotes)
		if !changed {
			continue
		}
		if _, err := tx.Exec(`UPDATE notes SET content = ?, updated_at = datetime('now')
			WHERE rowid = ?`, rewritten, id); err != nil {
			return err
		}
		if err := replaceNoteLinks(tx, id, rewritten); err != nil {
			return err
		}
	}
	return nil
}

func rewriteLinksForMove(content string, oldSource, newSource indexedNote, oldByPath, oldByTitle, newByPath, newByTitle map[string]int64, oldNotes, newNotes map[int64]indexedNote) (string, bool) {
	return rewriteContentOutsideCode(content,
		func(line string) (string, bool) {
			return rewriteLinkDefinitionLineForMove(line, oldSource, newSource,
				oldByPath, oldByTitle, newByPath, newByTitle, oldNotes, newNotes)
		},
		func(segment string) (string, bool) {
			return rewriteLinksInTextForMove(segment, oldSource, newSource,
				oldByPath, oldByTitle, newByPath, newByTitle, oldNotes, newNotes)
		})
}

func rewriteLinksInTextForMove(value string, oldSource, newSource indexedNote, oldByPath, oldByTitle, newByPath, newByTitle map[string]int64, oldNotes, newNotes map[int64]indexedNote) (string, bool) {
	value, wikilinkChanged := rewriteWikilinksInTextForMove(value, oldSource, newSource,
		oldByPath, oldByTitle, newByPath, newByTitle, oldNotes, newNotes)
	value, markdownChanged := rewriteMarkdownLinksInTextForMove(value, oldSource, newSource,
		oldByPath, oldByTitle, newByPath, newByTitle, oldNotes, newNotes)
	return value, wikilinkChanged || markdownChanged
}

func rewriteWikilinksInTextForMove(value string, oldSource, newSource indexedNote, oldByPath, oldByTitle, newByPath, newByTitle map[string]int64, oldNotes, newNotes map[int64]indexedNote) (string, bool) {
	matches := wikilinkPreviewPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, false
	}
	var result strings.Builder
	cursor := 0
	changed := false
	for _, match := range matches {
		rawTarget := value[match[2]:match[3]]
		target := strings.TrimSpace(rawTarget)
		oldTargetID := resolveNoteLink(oldSource, target, oldByPath, oldByTitle)
		newTarget, needsRelink := linkMoveInfo(target, oldSource, newSource, oldTargetID,
			oldByPath, oldByTitle, newByPath, newByTitle, oldNotes, newNotes)
		if oldTargetID == 0 || !needsRelink {
			continue
		}
		replacement := movedLinkTarget(rawTarget, newSource, newTarget, false)
		if replacement == rawTarget {
			continue
		}
		result.WriteString(value[cursor:match[2]])
		result.WriteString(replacement)
		cursor = match[3]
		changed = true
	}
	if !changed {
		return value, false
	}
	result.WriteString(value[cursor:])
	return result.String(), true
}

func rewriteMarkdownLinksInTextForMove(value string, oldSource, newSource indexedNote, oldByPath, oldByTitle, newByPath, newByTitle map[string]int64, oldNotes, newNotes map[int64]indexedNote) (string, bool) {
	matches := markdownInlineLinkPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, false
	}
	var result strings.Builder
	cursor := 0
	changed := false
	for _, match := range matches {
		rawDestination := value[match[4]:match[5]]
		destination := rawDestination
		angleBracketed := strings.HasPrefix(destination, "<") && strings.HasSuffix(destination, ">")
		if angleBracketed {
			destination = destination[1 : len(destination)-1]
		}
		oldTargetID := resolveNoteLink(oldSource, destination, oldByPath, oldByTitle)
		newTarget, needsRelink := linkMoveInfo(destination, oldSource, newSource, oldTargetID,
			oldByPath, oldByTitle, newByPath, newByTitle, oldNotes, newNotes)
		if oldTargetID == 0 || !needsRelink {
			continue
		}
		replacement := movedLinkTarget(destination, newSource, newTarget, !angleBracketed)
		if angleBracketed {
			replacement = "<" + replacement + ">"
		}
		if replacement == rawDestination {
			continue
		}
		result.WriteString(value[cursor:match[4]])
		result.WriteString(replacement)
		cursor = match[5]
		changed = true
	}
	if !changed {
		return value, false
	}
	result.WriteString(value[cursor:])
	return result.String(), true
}

func rewriteLinkDefinitionLineForMove(line string, oldSource, newSource indexedNote, oldByPath, oldByTitle, newByPath, newByTitle map[string]int64, oldNotes, newNotes map[int64]indexedNote) (string, bool) {
	match := markdownLinkDefinitionPattern.FindStringSubmatchIndex(line)
	if match == nil {
		return line, false
	}
	rawDestination := line[match[4]:match[5]]
	destination := rawDestination
	angleBracketed := strings.HasPrefix(destination, "<") && strings.HasSuffix(destination, ">")
	if angleBracketed {
		destination = destination[1 : len(destination)-1]
	}
	oldTargetID := resolveNoteLink(oldSource, destination, oldByPath, oldByTitle)
	newTarget, needsRelink := linkMoveInfo(destination, oldSource, newSource, oldTargetID,
		oldByPath, oldByTitle, newByPath, newByTitle, oldNotes, newNotes)
	if oldTargetID == 0 || !needsRelink {
		return line, false
	}
	replacement := movedLinkTarget(destination, newSource, newTarget, !angleBracketed)
	if angleBracketed {
		replacement = "<" + replacement + ">"
	}
	if replacement == rawDestination {
		return line, false
	}
	return line[:match[4]] + replacement + line[match[5]:], true
}

// linkMoveInfo decides whether a link must be rewritten after a move. Path
// targets are rebased whenever the source or the target changed folders, even
// if the old path still resolves through the title fallback. Title-only
// targets are only rewritten when their resolution actually changed, which
// happens when a duplicate title starts shadowing the original note.
func linkMoveInfo(target string, oldSource, newSource indexedNote, oldTargetID int64, oldByPath, oldByTitle, newByPath, newByTitle map[string]int64, oldNotes, newNotes map[int64]indexedNote) (indexedNote, bool) {
	if oldTargetID == 0 {
		return indexedNote{}, false
	}
	oldTarget, exists := oldNotes[oldTargetID]
	if !exists {
		return indexedNote{}, false
	}
	newTarget, exists := newNotes[oldTargetID]
	if !exists {
		return indexedNote{}, false
	}
	if pathStyleTarget(target) {
		if oldSource.Folder == newSource.Folder && oldTarget.Folder == newTarget.Folder {
			return indexedNote{}, false
		}
		return newTarget, true
	}
	if resolveNoteLink(newSource, target, newByPath, newByTitle) == oldTargetID {
		return indexedNote{}, false
	}
	return newTarget, true
}

func pathStyleTarget(target string) bool {
	core := target
	if index := strings.IndexAny(core, "?#"); index >= 0 {
		core = core[:index]
	}
	return strings.Contains(core, "/")
}

// movedLinkTarget builds a target that still points to newTarget from the new
// location of the source note, preserving the original shape (title-only,
// relative or absolute path, .md extension, fragment and query string).
func movedLinkTarget(raw string, newSource, newTarget indexedNote, escape bool) string {
	leading := raw[:len(raw)-len(strings.TrimLeft(raw, " "))]
	trailing := raw[len(strings.TrimRight(raw, " ")):]
	target := strings.TrimSpace(raw)

	suffix := ""
	if index := strings.IndexAny(target, "?#"); index >= 0 {
		suffix = target[index:]
		target = target[:index]
	}
	extension := ""
	if strings.HasSuffix(strings.ToLower(target), ".md") {
		extension = target[len(target)-3:]
		target = target[:len(target)-3]
	}

	fullPath := path.Join(newTarget.Folder, newTarget.Title)
	absolute := strings.HasPrefix(target, "/")
	relativePrefix := strings.HasPrefix(target, "./")
	var destination string
	if absolute {
		destination = "/" + fullPath
	} else {
		destination = relativeNotePath(newSource.Folder, fullPath)
		if relativePrefix {
			destination = "./" + destination
		}
	}
	if escape {
		destination = escapeLinkPath(destination)
	}
	return leading + destination + extension + suffix + trailing
}

func relativeNotePath(fromFolder, targetPath string) string {
	from := make([]string, 0)
	if fromFolder != "" {
		from = strings.Split(fromFolder, "/")
	}
	to := strings.Split(targetPath, "/")
	common := 0
	for common < len(from) && common < len(to) && from[common] == to[common] {
		common++
	}
	parts := make([]string, 0, len(from)-common+len(to)-common)
	for index := common; index < len(from); index++ {
		parts = append(parts, "..")
	}
	parts = append(parts, to[common:]...)
	if len(parts) == 0 {
		return path.Base(targetPath)
	}
	return path.Join(parts...)
}

func escapeLinkPath(value string) string {
	parts := strings.Split(value, "/")
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			continue
		}
		parts[index] = escapeLinkDestination(part)
	}
	return strings.Join(parts, "/")
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
	// Attachment and API URLs live in the same origin but are not notes.
	if strings.HasPrefix(target, "/api/") {
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
