package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// --- Rule: TextToImageRule ---
// Renders the entire conversation as a chat-style HTML document and converts
// it to images (one per page). The original entries are replaced with a compact
// set: a user message containing the page images followed by the last
// assistant+user pair (kept as text so Claude can continue naturally).

type TextToImageRule struct{}

func (r *TextToImageRule) Name() string { return "text-to-image" }
func (r *TextToImageRule) Description() string {
	return "Render conversation as chat images via weasyprint (experimental)"
}

func (r *TextToImageRule) Apply(entries []*JSONLEntry) ([]*JSONLEntry, CompactRuleReport) {
	report := CompactRuleReport{BytesBefore: entriesSize(entries)}

	if _, err := exec.LookPath("weasyprint"); err != nil {
		report.Details = append(report.Details, "weasyprint not found in PATH, skipping")
		report.BytesAfter = report.BytesBefore
		return entries, report
	}
	if _, err := exec.LookPath("magick"); err != nil {
		report.Details = append(report.Details, "magick not found in PATH, skipping")
		report.BytesAfter = report.BytesBefore
		return entries, report
	}

	// We keep the last assistant+user turn pair as text (so Claude can continue).
	// Everything before that gets rendered as images.
	splitIdx := findImageBoundary(entries)
	if splitIdx < 2 {
		report.Details = append(report.Details, "not enough entries to convert")
		report.BytesAfter = report.BytesBefore
		return entries, report
	}

	toRender := entries[:splitIdx]
	toKeep := entries[splitIdx:]

	// Build chat HTML from entries to render
	chatHTML := buildChatHTML(toRender)

	// Render to page images
	pngPages, err := renderHTMLToImages(chatHTML)
	if err != nil {
		report.Details = append(report.Details, fmt.Sprintf("render failed: %v", err))
		report.BytesAfter = report.BytesBefore
		return entries, report
	}

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
	// Add a text block explaining this is a rendered conversation
	imgBlocks = append(imgBlocks, map[string]string{
		"type": "text",
		"text": fmt.Sprintf("[conversation history rendered as %d page image(s)]", len(pngPages)),
	})
	imgContent, _ := json.Marshal(imgBlocks)

	// Create a new user entry containing the images.
	// Use the first entry's session ID and parent chain.
	sessionID := ""
	if len(toKeep) > 0 {
		sessionID = toKeep[0].SessionID
	} else if len(toRender) > 0 {
		sessionID = toRender[0].SessionID
	}

	imgEntry := NewEntry(
		generateUUID(),
		"", // root — no parent
		"user",
		sessionID,
		NewMessageContentBlocks("user", imgContent),
	)

	// Re-link: first kept entry's parent → image entry
	var result []*JSONLEntry
	result = append(result, imgEntry)
	for i, e := range toKeep {
		if i == 0 && e.UUID != "" {
			e.SetParentUUID(imgEntry.UUID)
		}
		result = append(result, e)
	}

	report.BytesAfter = entriesSize(result)
	report.BytesSaved = report.BytesBefore - report.BytesAfter
	report.EntriesRemoved = len(entries) - len(result)
	report.Details = append(report.Details,
		fmt.Sprintf("rendered %d entries as %d page image(s), kept %d trailing entries",
			len(toRender), len(pngPages), len(toKeep)))

	return result, report
}

// findImageBoundary returns the index where we stop rendering and start keeping
// text entries. We keep the last assistant message and any user messages after it.
func findImageBoundary(entries []*JSONLEntry) int {
	// Walk backwards to find the last assistant message
	lastAssistant := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Message != nil && entries[i].Type == "assistant" {
			lastAssistant = i
			break
		}
	}
	if lastAssistant < 0 {
		return 0
	}
	return lastAssistant
}

// buildChatSections renders entries to HTML section strings (one per entry).
func buildChatSections(entries []*JSONLEntry) string {
	var sections strings.Builder
	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		role := e.Message.Role
		if role == "" {
			role = e.Type
		}

		var blocks []json.RawMessage
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			var s string
			if json.Unmarshal(e.Message.Content, &s) == nil && s != "" {
				sections.WriteString(renderChatBubble(role, html.EscapeString(s)))
			}
			continue
		}

		for _, block := range blocks {
			var b map[string]json.RawMessage
			if json.Unmarshal(block, &b) != nil {
				continue
			}
			var typ string
			json.Unmarshal(b["type"], &typ)

			switch typ {
			case "text":
				var text string
				json.Unmarshal(b["text"], &text)
				if text != "" {
					sections.WriteString(renderChatBubble(role, html.EscapeString(text)))
				}
			case "tool_use":
				var name string
				json.Unmarshal(b["name"], &name)
				var input map[string]any
				json.Unmarshal(b["input"], &input)
				sections.WriteString(renderToolBlock("tool_use", formatToolUse(name, input)))
			case "tool_result":
				text := extractToolResultText(b["content"])
				if text != "" {
					if len(text) > 3000 {
						text = text[:3000] + "\n... (truncated)"
					}
					sections.WriteString(renderToolBlock("tool_result", html.EscapeString(text)))
				}
			}
		}
	}
	return sections.String()
}

const chatHTMLStyle = `@page {
  size: 1200px 900px;
  margin: 0;
}
body {
  font-family: -apple-system, "Helvetica Neue", Arial, sans-serif;
  font-size: 10px;
  line-height: 1.2;
  color: #1a1a1a;
  background: #f5f5f5;
  margin: 0;
  padding: 6px;
}
.bubble {
  margin: 2px 0;
  padding: 4px 8px;
  border-radius: 6px;
  max-width: 95%%;
  word-wrap: break-word;
  white-space: pre-wrap;
}
.assistant {
  background: #ffffff;
  border: 1px solid #e0e0e0;
}
.user {
  background: #e3f2fd;
  border: 1px solid #bbdefb;
}
.role-label {
  font-size: 8px;
  font-weight: bold;
  color: #888;
  margin: 4px 0 1px 4px;
}
.tool {
  font-family: "SF Mono", "Menlo", "Consolas", monospace;
  font-size: 9px;
  line-height: 1.15;
  background: #f8f8f8;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 3px 6px;
  margin: 1px 0;
  white-space: pre-wrap;
  word-wrap: break-word;
  color: #333;
}`

func wrapChatHTML(body string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><style>%s</style></head>
<body>%s</body></html>`, chatHTMLStyle, body)
}

// buildChatHTML renders conversation entries as a chat-style HTML document.
func buildChatHTML(entries []*JSONLEntry) string {
	return wrapChatHTML(buildChatSections(entries))
}

// renderHTMLChunksParallel splits entries into chunks, renders each in parallel.
// progressFn is called after each chunk completes: progressFn(completed, total).
func renderHTMLChunksParallel(entries []*JSONLEntry, chunkSize int, progressFn func(int, int)) ([][]byte, error) {
	// Split entries into chunks
	var chunks [][]*JSONLEntry
	for i := 0; i < len(entries); i += chunkSize {
		end := i + chunkSize
		if end > len(entries) {
			end = len(entries)
		}
		chunks = append(chunks, entries[i:end])
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no entries to render")
	}

	// Render each chunk's HTML
	htmlDocs := make([]string, len(chunks))
	for i, chunk := range chunks {
		htmlDocs[i] = wrapChatHTML(buildChatSections(chunk))
	}

	// Render all chunks in parallel
	type chunkResult struct {
		index int
		pages [][]byte
		err   error
	}
	results := make([]chunkResult, len(htmlDocs))
	var wg sync.WaitGroup
	completed := 0
	var mu sync.Mutex

	for i, doc := range htmlDocs {
		wg.Add(1)
		go func(idx int, html string) {
			defer wg.Done()
			start := time.Now()
			pages, err := renderHTMLToImages(html)
			fmt.Fprintf(os.Stderr, "  chunk %d: %v (%d pages)\n", idx, time.Since(start).Round(time.Millisecond), len(pages))
			results[idx] = chunkResult{index: idx, pages: pages, err: err}
			mu.Lock()
			completed++
			if progressFn != nil {
				progressFn(completed, len(htmlDocs))
			}
			mu.Unlock()
		}(i, doc)
	}
	wg.Wait()

	// Collect all pages in order
	var allPages [][]byte
	for _, r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("chunk %d: %w", r.index, r.err)
		}
		allPages = append(allPages, r.pages...)
	}
	return allPages, nil
}

// renderHTMLToImages renders a single HTML to PNG images.
func renderHTMLToImages(htmlContent string) ([][]byte, error) {
	tmpDir, err := os.MkdirTemp("", "surgery-img-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	htmlPath := filepath.Join(tmpDir, "input.html")
	pdfPath := filepath.Join(tmpDir, "output.pdf")
	pngPattern := filepath.Join(tmpDir, "output.png")

	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0644); err != nil {
		return nil, err
	}

	// weasyprint HTML → PDF
	cmd := exec.Command("weasyprint", htmlPath, pdfPath, "--presentational-hints")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("weasyprint: %v: %s", err, stderr.String())
	}

	// magick PDF → PNG per page, trimmed
	cmd = exec.Command("magick", "-density", "144", pdfPath, "-trim", "+repage", pngPattern)
	stderr.Reset()
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("magick: %v: %s", err, stderr.String())
	}

	// Collect pages
	var pages [][]byte
	if data, err := os.ReadFile(pngPattern); err == nil {
		pages = append(pages, data)
	} else {
		for i := 0; ; i++ {
			data, err := os.ReadFile(filepath.Join(tmpDir, fmt.Sprintf("output-%d.png", i)))
			if err != nil {
				break
			}
			pages = append(pages, data)
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no PNG output generated")
	}
	return pages, nil
}

func renderChatBubble(role, content string) string {
	return fmt.Sprintf(`<div class="role-label">%s</div><div class="bubble %s">%s</div>`+"\n",
		html.EscapeString(role), html.EscapeString(role), content)
}

func renderToolBlock(kind, content string) string {
	label := "tool_use"
	if kind == "tool_result" {
		label = "result"
	}
	return fmt.Sprintf(`<div class="role-label">%s</div><div class="tool">%s</div>`+"\n",
		label, content)
}

func formatToolUse(name string, input map[string]any) string {
	switch name {
	case "Read":
		fp, _ := input["file_path"].(string)
		return html.EscapeString(fmt.Sprintf("Read(%s)", fp))
	case "Write":
		fp, _ := input["file_path"].(string)
		return html.EscapeString(fmt.Sprintf("Write(%s)", fp))
	case "Edit":
		fp, _ := input["file_path"].(string)
		return html.EscapeString(fmt.Sprintf("Edit(%s)", fp))
	case "Bash":
		cmd, _ := input["command"].(string)
		if len(cmd) > 200 {
			cmd = cmd[:200] + "..."
		}
		return html.EscapeString(fmt.Sprintf("Bash: %s", cmd))
	case "Grep":
		pat, _ := input["pattern"].(string)
		return html.EscapeString(fmt.Sprintf("Grep(%s)", pat))
	case "Glob":
		pat, _ := input["pattern"].(string)
		return html.EscapeString(fmt.Sprintf("Glob(%s)", pat))
	default:
		b, _ := json.Marshal(input)
		s := string(b)
		if len(s) > 200 {
			s = s[:200] + "..."
		}
		return html.EscapeString(fmt.Sprintf("%s(%s)", name, s))
	}
}


