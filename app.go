package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx            context.Context
	startupProject string
	startupSession string
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

type StartupArgs struct {
	Project string `json:"project"`
	Session string `json:"session"`
}

func (a *App) GetStartupArgs() StartupArgs {
	return StartupArgs{Project: a.startupProject, Session: a.startupSession}
}

func claudeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func decodeProjectName(id string) string {
	re := regexp.MustCompile(`^-Users-[^-]+`)
	name := re.ReplaceAllString(id, "~")
	return strings.ReplaceAll(name, "-", "/")
}

type Project struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	SessionCount int     `json:"session_count"`
	Mtime        float64 `json:"mtime"`
}

type Session struct {
	ID       string  `json:"id"`
	Preview  string  `json:"preview"`
	MsgCount int     `json:"msg_count"`
	Size     int64   `json:"size"`
	Mtime    float64 `json:"mtime"`
}

type ContentSummary struct {
	Types       []string `json:"types"`
	TextPreview string   `json:"text_preview"`
	Size        int      `json:"size"`
}

type Message struct {
	UUID           string          `json:"uuid"`
	ParentUUID     string          `json:"parentUuid"`
	Type           string          `json:"type"`
	Role           string          `json:"role"`
	Timestamp      string          `json:"timestamp"`
	IsSidechain    bool            `json:"isSidechain"`
	ContentSummary ContentSummary  `json:"content_summary"`
	IsToolOnly     bool            `json:"is_tool_only"`
	IsSystem       bool            `json:"is_system"`
	Model          string          `json:"model"`
	Raw            json.RawMessage `json:"raw"`
}

type Conversation struct {
	Messages  []Message `json:"messages"`
	TotalSize int64     `json:"total_size"`
	SessionID string    `json:"session_id"`
}

type SaveRequest struct {
	KeepUUIDs    []string `json:"keep_uuids"`
	DeletedUUIDs []string `json:"deleted_uuids"` // for parentUuid repair after insertion
	InsertLines  []string `json:"insert_lines"`  // pre-built JSONL lines to insert at deletion gap
}

type SaveResult struct {
	Success   bool   `json:"success"`
	KeptLines int    `json:"kept_lines"`
	NewSize   int64  `json:"new_size"`
	Backup    string `json:"backup"`
}

func parseContentSummary(content json.RawMessage) ContentSummary {
	if content == nil {
		return ContentSummary{}
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		preview := s
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return ContentSummary{Types: []string{"text"}, TextPreview: preview, Size: len(s)}
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(content, &arr); err == nil {
		var types []string
		var preview string
		size := 0
		for _, item := range arr {
			var block map[string]json.RawMessage
			if err := json.Unmarshal(item, &block); err != nil {
				continue
			}
			var t string
			json.Unmarshal(block["type"], &t)
			types = append(types, t)
			if t == "text" && preview == "" {
				var text string
				json.Unmarshal(block["text"], &text)
				if len(text) > 200 {
					preview = text[:200]
				} else {
					preview = text
				}
			}
			size += len(item)
		}
		return ContentSummary{Types: types, TextPreview: preview, Size: size}
	}
	return ContentSummary{}
}

func mtimeOf(path string) float64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return float64(info.ModTime().UnixMilli()) / 1000.0
}

func (a *App) ListProjects() []Project {
	base := claudeDir()
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var projects []Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		jsonls, _ := filepath.Glob(filepath.Join(base, id, "*.jsonl"))
		if len(jsonls) == 0 {
			continue
		}
		var maxMtime float64
		for _, f := range jsonls {
			if m := mtimeOf(f); m > maxMtime {
				maxMtime = m
			}
		}
		projects = append(projects, Project{
			ID:           id,
			Name:         decodeProjectName(id),
			SessionCount: len(jsonls),
			Mtime:        maxMtime,
		})
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Mtime > projects[j].Mtime
	})
	return projects
}

func (a *App) ListSessions(projectID string) ([]Session, error) {
	dir := filepath.Join(claudeDir(), projectID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("project not found: %s", projectID)
	}
	var sessions []Session
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, _ := e.Info()
		id := strings.TrimSuffix(e.Name(), ".jsonl")

		var preview string
		msgCount := 0
		entries, _ := ReadJSONLFile(path)
		for _, e := range entries {
			if !e.IsMessage() {
				continue
			}
			msgCount++
			if preview == "" && e.Type == "user" && e.Message != nil {
				cs := parseContentSummary(e.Message.Content)
				if !strings.HasPrefix(cs.TextPreview, "<") && len(cs.TextPreview) > 5 {
					preview = cs.TextPreview
				}
			}
		}
		if preview == "" {
			preview = "(no text)"
		}
		sessions = append(sessions, Session{
			ID:       id,
			Preview:  preview,
			MsgCount: msgCount,
			Size:     info.Size(),
			Mtime:    mtimeOf(path),
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Mtime > sessions[j].Mtime
	})
	return sessions, nil
}

func (a *App) GetConversation(projectID, sessionID string) (*Conversation, error) {
	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return nil, err
	}

	var messages []Message
	for _, e := range entries {
		if !e.IsMessage() {
			continue
		}
		var cs ContentSummary
		var model string
		if e.Message != nil {
			cs = parseContentSummary(e.Message.Content)
			model = e.Message.Model
		}

		isToolOnly := len(cs.Types) > 0
		for _, ct := range cs.Types {
			if ct != "tool_result" && ct != "tool_use" {
				isToolOnly = false
				break
			}
		}
		var contentStr string
		if e.Message != nil {
			json.Unmarshal(e.Message.Content, &contentStr)
		}
		isSystem := strings.HasPrefix(contentStr, "<")

		role := e.Type
		if e.Message != nil {
			role = e.Message.Role
		}

		raw, _ := e.Marshal()

		messages = append(messages, Message{
			UUID:           e.UUID,
			ParentUUID:     e.GetParentUUID(),
			Type:           e.Type,
			Role:           role,
			Timestamp:      e.Timestamp,
			IsSidechain:    e.IsSidechain,
			ContentSummary: cs,
			IsToolOnly:     isToolOnly,
			IsSystem:       isSystem,
			Model:          model,
			Raw:            raw,
		})
	}
	return &Conversation{Messages: messages, TotalSize: info.Size(), SessionID: sessionID}, nil
}

func (a *App) SaveConversation(projectID, sessionID string, req SaveRequest) (*SaveResult, error) {
	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	keepSet := make(map[string]bool)
	for _, u := range req.KeepUUIDs {
		keepSet[u] = true
	}

	uuidToParent := map[string]string{}
	for _, e := range entries {
		if e.UUID != "" {
			uuidToParent[e.UUID] = e.GetParentUUID()
		}
	}

	// Build the set of all UUIDs that will survive in the output
	survivingUUIDs := make(map[string]bool)
	for k := range keepSet {
		survivingUUIDs[k] = true
	}
	for _, e := range entries {
		if !e.IsMessage() && e.UUID != "" {
			survivingUUIDs[e.UUID] = true
		}
	}

	findSurvivingAncestor := func(uuid string) string {
		visited := map[string]bool{}
		cur := uuid
		for cur != "" && !visited[cur] {
			visited[cur] = true
			if survivingUUIDs[cur] {
				return cur
			}
			cur = uuidToParent[cur]
		}
		return ""
	}

	// Build deleted set for insert-gap detection
	deletedSet := make(map[string]bool)
	for _, u := range req.DeletedUUIDs {
		deletedSet[u] = true
	}

	// Parse inserted lines as entries and find last insert UUID
	var insertEntries []*JSONLEntry
	var lastInsertUUID string
	for _, il := range req.InsertLines {
		ie, err := ParseEntry([]byte(il))
		if err == nil {
			insertEntries = append(insertEntries, ie)
			lastInsertUUID = ie.UUID
			survivingUUIDs[ie.UUID] = true
		}
	}

	// Helper to fix parentUuid if needed
	fixParentUuid := func(e *JSONLEntry) {
		parent := e.GetParentUUID()
		if parent == "" || survivingUUIDs[parent] {
			return
		}
		if deletedSet[parent] && lastInsertUUID != "" {
			e.SetParentUUID(lastInsertUUID)
			return
		}
		ancestor := findSurvivingAncestor(parent)
		if ancestor == "" {
			e.ParentUUID = nil
		} else {
			e.SetParentUUID(ancestor)
		}
	}

	var output []*JSONLEntry
	insertedAtGap := false
	for _, e := range entries {
		if !e.IsMessage() {
			fixParentUuid(e)
			output = append(output, e)
			continue
		}
		if !keepSet[e.UUID] {
			if !insertedAtGap && len(insertEntries) > 0 {
				output = append(output, insertEntries...)
				insertedAtGap = true
			}
			continue
		}
		fixParentUuid(e)
		output = append(output, e)
	}

	backup := path + ".bak"
	src, _ := os.Open(path)
	dst, _ := os.Create(backup)
	io.Copy(dst, src)
	src.Close()
	dst.Close()

	if err := WriteJSONLFile(path, output); err != nil {
		return nil, err
	}

	newInfo, _ := os.Stat(path)
	return &SaveResult{
		Success:   true,
		KeptLines: len(output),
		NewSize:   newInfo.Size(),
		Backup:    backup,
	}, nil
}

// EditMessage updates the text content of a specific message.
func (a *App) EditMessage(projectID, sessionID, uuid, newText string) error {
	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if e.UUID != uuid {
			continue
		}
		if e.Message == nil {
			continue
		}

		// Update content: replace text block(s) with newText
		content := e.Message.Content
		var s string
		if json.Unmarshal(content, &s) == nil {
			// String content: replace directly
			e.Message.Content, _ = json.Marshal(newText)
		} else {
			var arr []json.RawMessage
			if json.Unmarshal(content, &arr) == nil {
				first := true
				for i, item := range arr {
					var block map[string]json.RawMessage
					if json.Unmarshal(item, &block) != nil {
						continue
					}
					var t string
					json.Unmarshal(block["type"], &t)
					if t == "text" {
						if first {
							block["text"], _ = json.Marshal(newText)
							first = false
						} else {
							block["text"] = json.RawMessage(`""`)
						}
						arr[i], _ = json.Marshal(block)
					}
				}
				e.Message.Content, _ = json.Marshal(arr)
			}
		}
		break
	}

	return WriteJSONLFile(path, entries)
}

// setSidechainFrom sets isSidechain on all descendants of fromUUID (exclusive).
// If toSidechain=true, marks them as sidechain; false restores to main.
func (a *App) setSidechainFrom(projectID, sessionID, fromUUID string, toSidechain bool) error {
	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return err
	}

	uuidToParent := map[string]string{}
	for _, e := range entries {
		if e.UUID != "" {
			uuidToParent[e.UUID] = e.GetParentUUID()
		}
	}

	// Find all descendants (messages whose ancestor chain passes through fromUUID)
	isDescendant := map[string]bool{}
	for uuid := range uuidToParent {
		cur := uuid
		for cur != "" {
			if cur == fromUUID {
				isDescendant[uuid] = true
				break
			}
			cur = uuidToParent[cur]
		}
	}

	for _, e := range entries {
		if e.UUID != "" && isDescendant[e.UUID] {
			e.IsSidechain = toSidechain
		}
	}

	return WriteJSONLFile(path, entries)
}

func projectIDToPath(id string) string {
	return "/" + strings.ReplaceAll(strings.TrimPrefix(id, "-"), "-", "/")
}

// BuildClaudeCommand returns the shell command string for launching claude.
func BuildClaudeCommand(projectID, sessionID string, skipPermissions bool) string {
	dir := projectIDToPath(projectID)
	claudeCmd := "claude"
	if skipPermissions {
		claudeCmd = "claude --dangerously-skip-permissions"
	}
	if sessionID != "" {
		claudeCmd += " --resume " + sessionID
	}
	return fmt.Sprintf("cd %s && %s", dir, claudeCmd)
}

// GetClaudeCommand returns the command string so the frontend can display/copy it.
func (a *App) GetClaudeCommand(projectID, sessionID string, skipPermissions bool) string {
	return BuildClaudeCommand(projectID, sessionID, skipPermissions)
}

type terminalLauncher func(script string) error

var terminalLaunchers = map[string]terminalLauncher{
	"Terminal": func(script string) error {
		return exec.Command("osascript",
			"-e", `tell application "Terminal" to activate`,
			"-e", fmt.Sprintf(`tell application "Terminal" to do script %q`, script),
		).Run()
	},
	"iTerm2": func(script string) error {
		apple := fmt.Sprintf(`tell application "iTerm"
	activate
	set newWindow to (create window with default profile)
	tell current session of newWindow
		write text %q
	end tell
end tell`, script)
		return exec.Command("osascript", "-e", apple).Run()
	},
	"Ghostty": func(script string) error {
		return exec.Command("open", "-na", "Ghostty", "--args", "-e", "sh", "-c", script).Run()
	},
	"Alacritty": func(script string) error {
		return exec.Command("alacritty", "-e", "sh", "-c", script).Start()
	},
	"Kitty": func(script string) error {
		return exec.Command("kitty", "sh", "-c", script).Start()
	},
	"WezTerm": func(script string) error {
		return exec.Command("wezterm", "start", "--", "sh", "-c", script).Start()
	},
}

// GetAvailableTerminals returns terminal names that are installed.
func (a *App) GetAvailableTerminals() []string {
	// app bundles to check in /Applications
	appBundles := map[string]string{
		"iTerm2":    "iTerm.app",
		"Ghostty":   "Ghostty.app",
		"Alacritty": "Alacritty.app",
		"Kitty":     "kitty.app",
		"WezTerm":   "WezTerm.app",
	}
	// CLI commands to check in PATH
	cliNames := map[string]string{
		"Ghostty":   "ghostty",
		"Alacritty": "alacritty",
		"Kitty":     "kitty",
		"WezTerm":   "wezterm",
	}

	available := []string{"Terminal"} // Terminal.app is always available on macOS
	seen := map[string]bool{"Terminal": true}

	for name, bundle := range appBundles {
		if seen[name] {
			continue
		}
		if _, err := os.Stat(filepath.Join("/Applications", bundle)); err == nil {
			available = append(available, name)
			seen[name] = true
		}
	}
	for name, cli := range cliNames {
		if seen[name] {
			continue
		}
		if _, err := exec.LookPath(cli); err == nil {
			available = append(available, name)
			seen[name] = true
		}
	}
	sort.Strings(available)
	return available
}

func (a *App) ExecClaude(projectID string, sessionID string, skipPermissions bool, terminal string) error {
	script := BuildClaudeCommand(projectID, sessionID, skipPermissions)
	launcher, ok := terminalLaunchers[terminal]
	if !ok {
		launcher = terminalLaunchers["Terminal"]
	}
	return launcher(script)
}

// InsertMessage inserts a new message immediately after afterUUID.
// The message after afterUUID gets its parentUuid updated to the new message.
func (a *App) InsertMessage(projectID, sessionID, afterUUID, role, text string) error {
	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return err
	}

	newUUID := generateUUID()
	newEntry := NewEntry(newUUID, afterUUID, role, sessionID, NewMessageContent(role, text))

	// Insert after afterUUID
	var out []*JSONLEntry
	for _, e := range entries {
		out = append(out, e)
		if e.UUID == afterUUID {
			out = append(out, newEntry)
		}
	}

	// Update first child whose parentUuid == afterUUID to point to newUUID
	updatedChild := false
	for _, e := range out {
		if e.UUID == newUUID {
			continue
		}
		if e.GetParentUUID() == afterUUID && !updatedChild {
			e.SetParentUUID(newUUID)
			updatedChild = true
		}
	}

	return WriteJSONLFile(path, out)
}

func (a *App) BranchFrom(projectID, sessionID, fromUUID string) error {
	return a.setSidechainFrom(projectID, sessionID, fromUUID, true)
}

func (a *App) RestoreSidechain(projectID, sessionID, fromUUID string) error {
	return a.setSidechainFrom(projectID, sessionID, fromUUID, false)
}

// BranchNewSession copies all lines up to and including fromUUID into a new session file.
// Returns the new session ID.
func (a *App) BranchNewSession(projectID, sessionID, fromUUID string) (string, error) {
	srcPath := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(srcPath)
	if err != nil {
		return "", err
	}

	// Keep entries up to and including fromUUID
	var kept []*JSONLEntry
	for _, e := range entries {
		kept = append(kept, e)
		if e.UUID == fromUUID {
			break
		}
	}

	newID := generateUUID()
	dstPath := filepath.Join(claudeDir(), projectID, newID+".jsonl")
	if err := WriteJSONLFile(dstPath, kept); err != nil {
		return "", err
	}
	return newID, nil
}

// extractSelectedMessages returns raw message objects (role+content) for selected UUIDs, in file order.
// selectEntries returns entries matching the given UUID set, in file order.
func selectEntries(entries []*JSONLEntry, uuidSet map[string]bool) []*JSONLEntry {
	var result []*JSONLEntry
	for _, e := range entries {
		if uuidSet[e.UUID] {
			result = append(result, e)
		}
	}
	return result
}

// SummarizeMessages calls `claude -p` to summarize the selected messages.
func (a *App) SummarizeMessages(projectID, sessionID string, uuids []string) (string, error) {
	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return "", err
	}
	uuidSet := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		uuidSet[u] = true
	}
	selected := selectEntries(entries, uuidSet)
	if len(selected) == 0 {
		return "", fmt.Errorf("no selected messages found")
	}

	var parts []string
	for _, e := range selected {
		role := e.Type
		if e.Message != nil {
			role = e.Message.Role
		}
		text := ""
		if e.Message != nil {
			text = extractText(e.Message.Content)
		}
		if text == "" {
			text = "[no text]"
		}
		parts = append(parts, role+": "+text)
	}

	prompt := "以下の会話を簡潔にサマリーしてください。重要な決定・発見・コンテキストを保持してください。サマリーのみ出力してください。\n\n" +
		strings.Join(parts, "\n\n")

	out, err := runClaude(prompt, "")
	if err != nil {
		return "", fmt.Errorf("claude failed: %w", err)
	}
	return strings.TrimSpace(out), nil
}

const idealizeSystemPrompt = `You are an expert at cleaning up Claude Code conversation histories for context engineering.

You have two modes. Choose the best one for the situation:

MODE "actions" — Per-message triage. For each message, decide:
- "delete": errors, failed attempts, retries, unnecessary detours, verbose outputs that add no value
- "keep": essential context, correct approaches, important decisions
- "edit": messages worth keeping but with wasted content (trim unnecessary parts, fix errors). Provide the cleaned text in edited_content.
Use this mode when most messages can be kept as-is or simply deleted.

MODE "rewrite" — Full rewrite. Output a new sequence of messages (role + content) that replaces the entire selection.
Use this mode when the conversation is too messy for per-message triage and needs a clean rewrite.

Be aggressive about deleting waste. Preserve the minimum context needed to understand what happened and continue the work.`

// buildIdealizeSchema generates a JSON schema with mode field to choose actions or rewrite.
func buildIdealizeSchema(uuids []string) string {
	uuidEnum, _ := json.Marshal(uuids)
	return fmt.Sprintf(`{"type":"object","properties":{"mode":{"type":"string","enum":["actions","rewrite"]},"actions":{"type":"array","items":{"type":"object","properties":{"uuid":{"type":"string","enum":%s},"action":{"type":"string","enum":["delete","keep","edit"]},"edited_content":{"type":"string"}},"required":["uuid","action"]}},"messages":{"type":"array","items":{"type":"object","properties":{"role":{"type":"string","enum":["user","assistant"]},"content":{"type":"string"}},"required":["role","content"]}}},"required":["mode"]}`,
		string(uuidEnum))
}

// IdealizeMessages analyzes selected messages and returns either:
// - {"mode":"actions","actions":[{uuid,action,edited_content}]} for per-message triage
// - {"mode":"rewrite","messages":[{role,content}]} for full rewrite
func (a *App) IdealizeMessages(projectID, sessionID string, uuids []string) (string, error) {
	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return "", err
	}
	uuidSet := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		uuidSet[u] = true
	}

	type msgWithUUID struct {
		UUID    string          `json:"uuid"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	var labeled []msgWithUUID
	for _, e := range selectEntries(entries, uuidSet) {
		role := e.Type
		var content json.RawMessage
		if e.Message != nil {
			role = e.Message.Role
			content = e.Message.Content
		}
		labeled = append(labeled, msgWithUUID{UUID: e.UUID, Role: role, Content: content})
	}

	if len(labeled) == 0 {
		return "", fmt.Errorf("no selected messages found")
	}

	schema := buildIdealizeSchema(uuids)
	msgsJSON, _ := json.MarshalIndent(labeled, "", "  ")
	prompt := "Analyze each message and decide delete/keep/edit, or rewrite entirely:\n\n" + string(msgsJSON)

	out, err := runClaudeWithSystem(prompt, schema, idealizeSystemPrompt)
	if err != nil {
		return "", fmt.Errorf("claude failed: %w", err)
	}

	return out, nil
}

// ApplyIdealized creates a new session replacing the selected range with idealized messages.
// Returns the new session ID.
func (a *App) ApplyIdealized(projectID, sessionID string, uuids []string, messagesJSON string) (string, error) {
	var idealMsgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal([]byte(messagesJSON), &idealMsgs); err != nil {
		return "", fmt.Errorf("invalid messages JSON: %w", err)
	}

	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return "", err
	}

	uuidSet := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		uuidSet[u] = true
	}

	// Find parentUuid of first selected message
	var firstParentUUID string
	for _, e := range entries {
		if uuidSet[e.UUID] {
			firstParentUUID = e.GetParentUUID()
			break
		}
	}

	// Build new entries for idealized messages
	var newEntries []*JSONLEntry
	prevUUID := firstParentUUID
	for _, im := range idealMsgs {
		e := NewEntry(generateUUID(), prevUUID, im.Role, sessionID,
			NewMessageContentBlocks(im.Role, im.Content))
		newEntries = append(newEntries, e)
		prevUUID = e.UUID
	}
	lastIdealUUID := prevUUID

	// Build output: pre-selection + idealized + post-selection
	var output []*JSONLEntry
	inSelection := false
	passedSelection := false
	firstPostFixed := false
	for _, e := range entries {
		if uuidSet[e.UUID] {
			if !inSelection {
				inSelection = true
				output = append(output, newEntries...)
			}
			passedSelection = true
			continue
		}
		if passedSelection && !firstPostFixed {
			if uuidSet[e.GetParentUUID()] {
				e.SetParentUUID(lastIdealUUID)
				firstPostFixed = true
			}
		}
		output = append(output, e)
	}
	_ = inSelection

	newID := generateUUID()
	dstPath := filepath.Join(claudeDir(), projectID, newID+".jsonl")
	if err := WriteJSONLFile(dstPath, output); err != nil {
		return "", err
	}
	return newID, nil
}

func extractText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return strings.TrimSpace(s)
	}
	var arr []json.RawMessage
	if json.Unmarshal(content, &arr) != nil {
		return ""
	}
	var texts []string
	for _, item := range arr {
		var block map[string]json.RawMessage
		if json.Unmarshal(item, &block) != nil {
			continue
		}
		var t string
		json.Unmarshal(block["type"], &t)
		switch t {
		case "text":
			var text string
			json.Unmarshal(block["text"], &text)
			if text != "" {
				texts = append(texts, text)
			}
		case "tool_use":
			var name string
			json.Unmarshal(block["name"], &name)
			input, _ := json.Marshal(block["input"])
			texts = append(texts, fmt.Sprintf("[tool_use: %s %s]", name, string(input)))
		case "tool_result":
			var resultText string
			// content may be array or string
			var sub []json.RawMessage
			if json.Unmarshal(block["content"], &sub) == nil {
				for _, s := range sub {
					var sb map[string]json.RawMessage
					json.Unmarshal(s, &sb)
					var tx string
					json.Unmarshal(sb["text"], &tx)
					resultText += tx
				}
			} else {
				json.Unmarshal(block["content"], &resultText)
			}
			if len(resultText) > 500 {
				resultText = resultText[:500] + "…"
			}
			texts = append(texts, fmt.Sprintf("[tool_result: %s]", resultText))
		case "thinking":
			// skip thinking blocks
		}
	}
	return strings.Join(texts, "\n")
}

const summarizeSystemPrompt = `You are a conversation summarizer. Output only a concise summary of the conversation provided. Do not use any tools. Do not ask questions. Output the summary text directly with no preamble.`

// runClaude calls claude -p with --output-format json.
// If jsonSchema is non-empty, --json-schema is passed; systemPrompt overrides the default.
func runClaude(prompt, jsonSchema string) (string, error) {
	return runClaudeWithSystem(prompt, jsonSchema, "")
}

// runClaudeStreaming streams output tokens via Wails events and returns the final result.
func runClaudeStreaming(ctx context.Context, prompt, jsonSchema, systemPrompt string) (string, error) {
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--effort", "low", "--model", "claude-sonnet-4-6", "--max-turns", "1"}
	if jsonSchema != "" {
		args = append(args, "--json-schema", jsonSchema)
	}
	sp := systemPrompt
	if sp == "" {
		sp = summarizeSystemPrompt
	}
	args = append(args, "--system-prompt", sp, prompt)

	cmd := exec.Command("claude", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return "", err
	}

	var finalResult string
	var allLines []string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		allLines = append(allLines, line)
		var event map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		var evType string
		json.Unmarshal(event["type"], &evType)

		switch evType {
		case "assistant":
			if ctx != nil {
				runtime.EventsEmit(ctx, "claude:stream", line)
			}
		case "result":
			// structured_output has the JSON schema result; result is plain text
			if so, ok := event["structured_output"]; ok && string(so) != "null" {
				finalResult = string(so)
			} else {
				raw := event["result"]
				var s string
				if json.Unmarshal(raw, &s) == nil {
					finalResult = s
				} else {
					finalResult = string(raw)
				}
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		if se := strings.TrimSpace(stderrBuf.String()); se != "" {
			return "", fmt.Errorf("%s", se)
		}
		return "", err
	}
	if finalResult == "" && len(allLines) > 0 {
		return "", fmt.Errorf("no result event found. last lines: %s", allLines[len(allLines)-1])
	}
	return strings.TrimSpace(finalResult), nil
}

func runClaudeWithSystem(prompt, jsonSchema, systemPrompt string) (string, error) {
	args := []string{"-p", "--output-format", "json", "--effort", "low", "--model", "claude-sonnet-4-6", "--max-turns", "3", "--tools", ""}
	if jsonSchema != "" {
		args = append(args, "--json-schema", jsonSchema)
	}
	sp := systemPrompt
	if sp == "" {
		sp = summarizeSystemPrompt
	}
	args = append(args, "--system-prompt", sp, prompt)

	cmd := exec.Command("claude", args...)
	out, err := cmd.Output()
	if err != nil {
		// If we got output despite error (e.g. error_max_turns), try to parse it
		if ee, ok := err.(*exec.ExitError); ok {
			combined := out
			if len(combined) == 0 {
				combined = ee.Stderr
			}
			if len(combined) > 0 {
				// Try parsing as JSON - may contain structured_output despite exit error
				var envelope map[string]json.RawMessage
				if json.Unmarshal(combined, &envelope) == nil {
					if so, ok := envelope["structured_output"]; ok && string(so) != "null" {
						return string(so), nil
					}
					if r, ok := envelope["result"]; ok {
						var s string
						if json.Unmarshal(r, &s) == nil && s != "" {
							return strings.TrimSpace(s), nil
						}
					}
				}
				return "", fmt.Errorf("%s", strings.TrimSpace(string(combined)))
			}
		}
		return "", err
	}

	// Parse JSON envelope
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(out, &envelope); err != nil {
		return strings.TrimSpace(string(out)), nil
	}
	if errField, ok := envelope["error"]; ok {
		var e string
		json.Unmarshal(errField, &e)
		if e != "" {
			return "", fmt.Errorf("%s", e)
		}
	}
	// structured_output takes priority (for --json-schema)
	if so, ok := envelope["structured_output"]; ok && string(so) != "null" {
		return string(so), nil
	}
	var result string
	if r, ok := envelope["result"]; ok {
		if json.Unmarshal(r, &result) == nil {
			return strings.TrimSpace(result), nil
		}
		return string(r), nil
	}
	return strings.TrimSpace(string(out)), nil
}

// ApplySummary replaces the selected messages with a single summary user message.
func (a *App) ApplySummary(projectID, sessionID string, uuids []string, summary string) error {
	path := filepath.Join(claudeDir(), projectID, sessionID+".jsonl")
	entries, err := ReadJSONLFile(path)
	if err != nil {
		return err
	}

	uuidSet := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		uuidSet[u] = true
	}

	// Find parentUuid of first selected message
	var firstParentUUID string
	found := false
	for _, e := range entries {
		if uuidSet[e.UUID] {
			firstParentUUID = e.GetParentUUID()
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("selected messages not found")
	}

	summaryEntry := NewEntry(generateUUID(), firstParentUUID, "user", sessionID,
		NewMessageContent("user", "[Summary]\n"+summary))
	summaryEntry.IsSummary = true

	// Build output: replace selected block with summary, fix parentUuid references
	var output []*JSONLEntry
	inserted := false
	for _, e := range entries {
		if uuidSet[e.UUID] {
			if !inserted {
				output = append(output, summaryEntry)
				inserted = true
			}
			continue
		}
		if uuidSet[e.GetParentUUID()] {
			e.SetParentUUID(summaryEntry.UUID)
		}
		output = append(output, e)
	}

	return WriteJSONLFile(path, output)
}
