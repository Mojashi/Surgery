package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	args := os.Args[1:]

	switch {
	case len(args) >= 1 && (args[0] == "--version" || args[0] == "-v"):
		fmt.Println(Version)
		os.Exit(0)

	case len(args) >= 1 && args[0] == "update":
		runUpdate()

	case len(args) >= 1 && args[0] == "compact":
		runCompactCLI(args[1:])

	case len(args) >= 1 && args[0] == "--open":
		// Open Wails window, optionally with a specific session
		runGUI(argStr(args, 1), argStr(args, 2))

	case len(args) >= 1 && args[0] == "--bg":
		// Background: wait for token in JSONL, then run command
		// Args: --bg <token> <command> [extra-args...]
		runBg(args[1:])

	case len(args) >= 1 && args[0] == "--watch":
		// Legacy: redirect to --bg open
		runBg([]string{argStr(args, 1), "open"})

	case len(args) >= 1 && args[0] == "--compact-run":
		// Legacy: redirect to --bg compact
		runBg(append([]string{argStr(args, 1), "compact"}, args[2:]...))

	case len(args) >= 1 && args[0] == "--compact-window":
		// Open compact dialog with a JSONL path
		runCompactWindow(argStr(args, 1))

	case len(args) >= 1 && args[0] == "--notify":
		// Show a notification popup (title from arg, message from stdin)
		title := argStr(args, 1)
		if title == "" {
			title = "Surgery"
		}
		msg, _ := io.ReadAll(os.Stdin)
		runNotifyWindow(title, string(msg))

	default:
		if os.Getenv("CLAUDECODE") == "1" {
			// Inside Claude Code: token-based session detection
			runSurgery()
		} else {
			// Standalone: open GUI directly (auto-detect project from cwd)
			projectID := deriveProjectID()
			runGUI(projectID, "")
		}
	}
}

func argStr(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func deriveProjectID() string {
	cwd, _ := os.Getwd()
	return "-" + strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")
}

// spawnBackground prints a token for Claude Code session detection,
// then spawns a background process: --bg <token> <command> [args...]
func spawnBackground(command string, extraArgs ...string) {
	b := make([]byte, 8)
	rand.Read(b)
	token := "SURGERY_" + strings.ToUpper(hex.EncodeToString(b))
	fmt.Println(token)

	bgArgs := append([]string{"--bg", token, command}, extraArgs...)
	exe, _ := os.Executable()
	cmd := exec.Command(exe, bgArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Start()
	cmd.Process.Release()
	os.Exit(0)
}

func runSurgery() {
	spawnBackground("open")
}

// runBg is the unified background handler.
// Args: <token> <command> [extra-args...]
// It waits for the token to appear in a JSONL, then dispatches by command.
func runBg(args []string) {
	if len(args) < 2 {
		os.Exit(1)
	}
	token := args[0]
	command := args[1]

	projectsBase := filepath.Join(os.Getenv("HOME"), ".claude", "projects")

	// Wait for token to appear in a JSONL file
	var jsonlPath, foundProjectID string
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		jsonlPath, foundProjectID = findJSONLWithTokenAllProjects(token, projectsBase)
		if jsonlPath != "" {
			break
		}
	}
	if jsonlPath == "" {
		spawnNotify("Surgery", "Error: could not find session JSONL file.")
		return
	}
	sessionID := strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")

	switch command {
	case "open":
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "--open", foundProjectID, sessionID)
		cmd.Start()

	case "compact":
		spawnCompactWindow(jsonlPath)
	}
}

// spawnCompactWindow opens the compact dialog window (which runs processing inside).
func spawnCompactWindow(jsonlPath string) {
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "--compact-window", jsonlPath)
	cmd.Start()
}

func findJSONLWithTokenAllProjects(token, projectsBase string) (string, string) {
	projects, err := os.ReadDir(projectsBase)
	if err != nil {
		return "", ""
	}
	var bestPath, bestProject string
	var bestTime time.Time
	cutoff := time.Now().Add(-30 * time.Second)
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		projectDir := filepath.Join(projectsBase, p.Name())
		entries, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".jsonl" {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}
			path := filepath.Join(projectDir, e.Name())
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			const tailSize = 64 * 1024
			fi, _ := f.Stat()
			offset := fi.Size() - tailSize
			if offset < 0 {
				offset = 0
			}
			buf := make([]byte, tailSize)
			n, _ := f.ReadAt(buf, offset)
			f.Close()
			if strings.Contains(string(buf[:n]), token) && info.ModTime().After(bestTime) {
				bestPath = path
				bestProject = p.Name()
				bestTime = info.ModTime()
			}
		}
	}
	return bestPath, bestProject
}

func mostRecentJSONL(projectDir string) string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}
	var latest string
	var latestTime time.Time
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = filepath.Join(projectDir, e.Name())
		}
	}
	return latest
}

// mostRecentJSONLAllProjects finds the most recently modified JSONL across all project directories.
// Returns (path, projectID).
func mostRecentJSONLAllProjects(projectsBase string) (string, string) {
	projects, err := os.ReadDir(projectsBase)
	if err != nil {
		return "", ""
	}
	var bestPath, bestProject string
	var bestTime time.Time
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		projectDir := filepath.Join(projectsBase, p.Name())
		entries, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".jsonl" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(bestTime) {
				bestTime = info.ModTime()
				bestPath = filepath.Join(projectDir, e.Name())
				bestProject = p.Name()
			}
		}
	}
	return bestPath, bestProject
}

func runUpdate() {
	fmt.Printf("surgery %s\n", Version)
	fmt.Println("Checking for updates...")

	app := &App{}
	info, err := app.CheckUpdate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	fmt.Printf("Current: v%s  Latest: v%s\n", info.CurrentVersion, info.LatestVersion)

	if !info.HasUpdate {
		fmt.Println("Already up to date.")
		return
	}
	if info.DownloadURL == "" {
		fmt.Fprintln(os.Stderr, "No download URL found for this platform.")
		os.Exit(1)
	}

	fmt.Printf("Downloading v%s...\n", info.LatestVersion)
	if err := cliUpdate(info.DownloadURL, info.LatestVersion); err != nil {
		fmt.Fprintln(os.Stderr, "Update failed:", err)
		os.Exit(1)
	}
}

func cliUpdate(downloadURL, newVersion string) error {
	tmpZip := filepath.Join(os.TempDir(), "surgery-update.zip")
	if err := downloadFile(downloadURL, tmpZip); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	tmpDir := filepath.Join(os.TempDir(), "surgery-update")
	os.RemoveAll(tmpDir)
	os.MkdirAll(tmpDir, 0755)
	if err := unzip(tmpZip, tmpDir); err != nil {
		return fmt.Errorf("unzip: %w", err)
	}

	matches, _ := filepath.Glob(filepath.Join(tmpDir, "*.app"))
	if len(matches) == 0 {
		return fmt.Errorf("no .app found in zip")
	}
	newApp := matches[0]

	// Resolve current exe (follow symlink)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	// Walk up to find .app bundle
	appBundle := exe
	for !strings.HasSuffix(appBundle, ".app") {
		parent := filepath.Dir(appBundle)
		if parent == appBundle {
			// Not inside a .app — just replace the binary directly
			appBundle = exe
			newBin := filepath.Join(newApp, "Contents", "MacOS", "surgery")
			if err := exec.Command("cp", newBin, appBundle).Run(); err != nil {
				return fmt.Errorf("replace binary: %w", err)
			}
			fmt.Printf("Updated to v%s. Run surgery again.\n", newVersion)
			return nil
		}
		appBundle = parent
	}

	// Replace entire .app bundle
	os.RemoveAll(appBundle)
	if err := exec.Command("cp", "-r", newApp, appBundle).Run(); err != nil {
		return fmt.Errorf("replace .app: %w", err)
	}
	fmt.Printf("Updated to v%s. Run surgery again.\n", newVersion)
	return nil
}

func runCompactCLI(args []string) {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(os.Stderr, "Usage: !surgery compact")
			fmt.Fprintln(os.Stderr, "  Renders conversation history as images for token efficiency.")
			fmt.Fprintln(os.Stderr, "  Must be run inside Claude Code.")
			os.Exit(0)
		}
	}

	if os.Getenv("CLAUDECODE") != "1" {
		fmt.Fprintln(os.Stderr, "Error: surgery compact must be run inside Claude Code (!surgery compact)")
		os.Exit(1)
	}

	spawnBackground("compact")
}

// runCompactBackground is the background process spawned by "compact" under Claude Code.
// Args: token [--dry-run] [--rules rule1,rule2,...]
func runCompactBackground(args []string) {
	if len(args) < 1 {
		os.Exit(1)
	}
	token := args[0]
	dryRun := false
	for i := 1; i < len(args); i++ {
		if args[i] == "--dry-run" {
			dryRun = true
		}
	}
	ruleNames := []string{"text-to-image"}

	projectsBase := filepath.Join(os.Getenv("HOME"), ".claude", "projects")

	// Wait for token to appear in a JSONL file
	var jsonlPath string
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		jsonlPath, _ = findJSONLWithTokenAllProjects(token, projectsBase)
		if jsonlPath != "" {
			break
		}
	}
	if jsonlPath == "" {
		spawnNotify("Surgery Compact", "Error: could not find session JSONL file.")
		return
	}

	// Run compaction
	conv, err := LoadConversation(jsonlPath)
	if err != nil {
		spawnNotify("Surgery Compact", fmt.Sprintf("Error reading file: %v", err))
		return
	}
	beforeCount := conv.EntryCount()
	sessionID := strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")

	rules := selectRules(ruleNames)
	report := RunCompaction(conv, rules)

	var sb strings.Builder
	formatCompactReport(&sb, filepath.Base(jsonlPath), beforeCount, conv.EntryCount(), report)

	if dryRun {
		fmt.Fprintln(&sb, "\n(dry run — no changes written)")
		fmt.Fprintf(&sb, "\nResume command:\n/resume %s", sessionID)
	} else {
		newID := generateUUID()
		conv.SessionID = newID
		for _, e := range conv.Entries {
			e.SessionID = newID
		}
		dir := filepath.Dir(jsonlPath)
		newPath := filepath.Join(dir, newID+".jsonl")
		if err := conv.WriteToFile(newPath); err != nil {
			fmt.Fprintf(&sb, "\nError writing: %v\n", err)
		} else {
			fmt.Fprintf(&sb, "\nNew session: %s\n", newID)
			fmt.Fprintf(&sb, "Original untouched: %s\n", sessionID)
		}
		fmt.Fprintf(&sb, "\nResume command:\n/resume %s", newID)
	}

	spawnNotify("Surgery Compact", sb.String())
}

// runCompactOnFile runs compaction on a JSONL file, writing to a new session file.
// The original file is left untouched.
func runCompactOnFile(jsonlPath string, ruleNames []string, dryRun bool) {
	conv, err := LoadConversation(jsonlPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading file:", err)
		os.Exit(1)
	}
	beforeCount := conv.EntryCount()

	rules := selectRules(ruleNames)
	report := RunCompaction(conv, rules)

	sessionID := strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")
	printCompactReport(filepath.Base(jsonlPath), beforeCount, conv.EntryCount(), report)

	if dryRun {
		fmt.Println("\n(dry run — no changes written)")
		return
	}

	// Write to new session file in the same directory
	newID := generateUUID()
	conv.SessionID = newID
	// Update sessionId in all entries
	for _, e := range conv.Entries {
		e.SessionID = newID
	}
	dir := filepath.Dir(jsonlPath)
	newPath := filepath.Join(dir, newID+".jsonl")
	if err := conv.WriteToFile(newPath); err != nil {
		fmt.Fprintln(os.Stderr, "Error writing file:", err)
		os.Exit(1)
	}
	fmt.Printf("\nNew session: %s\n", newID)
	fmt.Printf("Original untouched: %s\n", sessionID)
}

func formatCompactReport(w *strings.Builder, filename string, beforeCount, afterCount int, report CompactReport) {
	fmt.Fprintf(w, "Compaction report for %s\n", filename)
	fmt.Fprintf(w, "  Before: %s (%d entries)\n", humanBytes(report.TotalBefore), beforeCount)
	fmt.Fprintf(w, "  After:  %s (%d entries)\n", humanBytes(report.TotalAfter), afterCount)
	fmt.Fprintf(w, "  Saved:  %s (%.1f%%)\n", humanBytes(report.TotalSaved),
		float64(report.TotalSaved)*100/float64(report.TotalBefore))
	fmt.Fprintln(w)
	for _, rr := range report.Rules {
		if rr.Report.BytesSaved > 0 || rr.Report.EntriesRemoved > 0 || len(rr.Report.Details) > 0 {
			fmt.Fprintf(w, "  [%s] saved %s, removed %d entries\n",
				rr.Name, humanBytes(rr.Report.BytesSaved), rr.Report.EntriesRemoved)
			for _, d := range rr.Report.Details {
				fmt.Fprintf(w, "    - %s\n", d)
			}
		} else {
			fmt.Fprintf(w, "  [%s] no changes\n", rr.Name)
		}
	}
}

func printCompactReport(filename string, beforeCount, afterCount int, report CompactReport) {
	var sb strings.Builder
	formatCompactReport(&sb, filename, beforeCount, afterCount, report)
	fmt.Print(sb.String())
}

func printCompactUsage() {
	fmt.Fprintln(os.Stderr, "Usage: surgery compact [session-id] [--dry-run] [--rules rule1,rule2,...]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Session resolution (in priority order):")
	fmt.Fprintln(os.Stderr, "  1. Explicit session ID argument")
	fmt.Fprintln(os.Stderr, "  2. Auto-detect via token when run as Claude Code subprocess (CLAUDECODE=1)")
	fmt.Fprintln(os.Stderr, "  3. Most recent session in the cwd-derived project")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Available rules:")
	for _, r := range allRulesIncludingExtras() {
		fmt.Fprintf(os.Stderr, "  %-25s %s\n", r.Name(), r.Description())
	}
}

func allRulesIncludingExtras() []CompactRule {
	seen := map[string]bool{}
	var all []CompactRule
	for _, r := range AllRules() {
		all = append(all, r)
		seen[r.Name()] = true
	}
	extras := []CompactRule{&StripFileHistoryRule{}, &StripErrorRetriesRule{}, &TruncateLargeBashRule{}, &TextToImageRule{}}
	for _, r := range extras {
		if !seen[r.Name()] {
			all = append(all, r)
		}
	}
	return all
}

// findSessionByID searches all projects for a JSONL file matching the session ID.
func findSessionByID(projectsBase, sessionID string) string {
	// Strip .jsonl extension if provided
	sessionID = strings.TrimSuffix(sessionID, ".jsonl")

	projects, err := os.ReadDir(projectsBase)
	if err != nil {
		return ""
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsBase, p.Name(), sessionID+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// --- Compact Window ---

type CompactApp struct {
	ctx      context.Context
	jsonlPath string
}

func (c *CompactApp) startup(ctx context.Context) { c.ctx = ctx }

// RunCompact is called from the frontend after the window is already visible.
// It runs the full compact pipeline with parallel rendering and progress events.
func (c *CompactApp) RunCompact() map[string]string {
	if c.jsonlPath == "" {
		return map[string]string{"error": "no session file"}
	}

	emit := func(msg string) {
		wailsRuntime.EventsEmit(c.ctx, "compact-progress", msg)
	}

	emit("Loading session...")
	conv, err := LoadConversation(c.jsonlPath)
	if err != nil {
		return map[string]string{"error": fmt.Sprintf("load error: %v", err)}
	}
	beforeCount := conv.EntryCount()

	splitIdx := findImageBoundary(conv.Entries)
	if splitIdx < 2 {
		return map[string]string{"error": "not enough entries to convert"}
	}
	toRender := conv.Entries[:splitIdx]
	toKeep := conv.Entries[splitIdx:]

	// Build preview HTML
	emit("Building HTML preview...")
	previewHTML := buildChatHTML(toRender)

	// Render images in parallel chunks (50 entries per chunk)
	emit("Rendering images (0%)...")
	pngPages, err := renderHTMLChunksParallel(toRender, 50, func(done, total int) {
		pct := done * 100 / total
		emit(fmt.Sprintf("Rendering images (%d%%, %d/%d chunks)...", pct, done, total))
	})
	if err != nil {
		return map[string]string{"error": fmt.Sprintf("render error: %v", err)}
	}

	emit("Building output...")

	// Build image content blocks
	var imgBlocks []any
	for _, pngData := range pngPages {
		imgBlocks = append(imgBlocks, map[string]any{
			"type": "image",
			"source": map[string]string{
				"type":       "base64",
				"media_type": "image/png",
				"data":       base64.StdEncoding.EncodeToString(pngData),
			},
		})
	}
	imgBlocks = append(imgBlocks, map[string]string{
		"type": "text",
		"text": fmt.Sprintf("[conversation history rendered as %d page image(s)]", len(pngPages)),
	})
	imgContent, _ := json.Marshal(imgBlocks)

	sessionID := ""
	if len(toKeep) > 0 {
		sessionID = toKeep[0].SessionID
	} else if len(toRender) > 0 {
		sessionID = toRender[0].SessionID
	}

	imgEntry := NewEntry(generateUUID(), "", "user", sessionID, NewMessageContentBlocks("user", imgContent))

	var result []*JSONLEntry
	result = append(result, imgEntry)
	for i, e := range toKeep {
		if i == 0 && e.UUID != "" {
			e.SetParentUUID(imgEntry.UUID)
		}
		result = append(result, e)
	}

	// Build report
	report := CompactReport{TotalBefore: entriesSize(conv.Entries), TotalAfter: entriesSize(result)}
	report.TotalSaved = report.TotalBefore - report.TotalAfter

	emit("Writing session...")

	// Write new session
	newConv := &Conversation{Entries: result}
	newID := generateUUID()
	newConv.SessionID = newID
	for _, e := range newConv.Entries {
		e.SessionID = newID
	}
	dir := filepath.Dir(c.jsonlPath)
	if err := newConv.WriteToFile(filepath.Join(dir, newID+".jsonl")); err != nil {
		return map[string]string{"error": fmt.Sprintf("write error: %v", err)}
	}

	var sb strings.Builder
	formatCompactReport(&sb, filepath.Base(c.jsonlPath), beforeCount, len(result), report)
	sb.WriteString(fmt.Sprintf("\n%d entries → %d entries, %d page images", len(conv.Entries), len(result), len(pngPages)))

	return map[string]string{
		"session_id":   newID,
		"html":         previewHTML,
		"report":       sb.String(),
		"resume_cmd":   "claude --resume " + newID,
		"resume_slash": "/resume " + newID,
	}
}

func runCompactWindow(jsonlPath string) {
	app := &CompactApp{jsonlPath: jsonlPath}
	wails.Run(&options.App{
		Title:            "Surgery Compact",
		Width:            1100,
		Height:           700,
		DisableResize:    false,
		BackgroundColour: &options.RGBA{R: 13, G: 17, B: 23, A: 1},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind:      []interface{}{app},
	})
}

// spawnNotify launches a new process with --notify to show a Wails popup.
func spawnNotify(title, message string) {
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "--notify", title)
	cmd.Stdin = strings.NewReader(message)
	cmd.Start()
}

func runNotifyWindow(title, message string) {
	notifyApp := &NotifyApp{title: title, message: message}
	wails.Run(&options.App{
		Title:            title,
		Width:            500,
		Height:           400,
		DisableResize:    false,
		BackgroundColour: &options.RGBA{R: 13, G: 17, B: 23, A: 1},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: notifyApp.startup,
		Bind:      []interface{}{notifyApp},
	})
}

type NotifyApp struct {
	ctx     context.Context
	title   string
	message string
}

func (n *NotifyApp) startup(ctx context.Context) {
	n.ctx = ctx
}

func (n *NotifyApp) GetNotification() map[string]string {
	return map[string]string{"title": n.title, "message": n.message}
}

func runGUI(startupProject, startupSession string) {
	app := NewApp()
	app.startupProject = startupProject
	app.startupSession = startupSession

	err := wails.Run(&options.App{
		Title:  "Surgery",
		Width:  1400,
		Height: 900,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 13, G: 17, B: 23, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}

func selectRules(names []string) []CompactRule {
	if len(names) == 0 {
		return AllRules()
	}
	all := map[string]CompactRule{}
	for _, r := range AllRules() {
		all[r.Name()] = r
	}
	// Also register non-default rules
	extras := []CompactRule{&StripFileHistoryRule{}, &StripErrorRetriesRule{}, &TruncateLargeBashRule{}, &TextToImageRule{}}
	for _, r := range extras {
		all[r.Name()] = r
	}
	var rules []CompactRule
	for _, n := range names {
		n = strings.TrimSpace(n)
		if r, ok := all[n]; ok {
			rules = append(rules, r)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: unknown rule %q, skipping\n", n)
		}
	}
	return rules
}

