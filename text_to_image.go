package main

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed scripts/html2png
var html2pngBin []byte

//go:embed scripts/pdf2png
var pdf2pngBin []byte

// --- Rule: TextToImageRule ---
// Renders the entire conversation as a chat-style HTML document and converts
// it to images (one per page). The original entries are replaced with a compact
// set: a user message containing the page images followed by the last
// assistant+user pair (kept as text so Claude can continue naturally).

type TextToImageRule struct{}

func (r *TextToImageRule) Name() string { return "text-to-image" }
func (r *TextToImageRule) Description() string {
	return "Render conversation as chat images via WebKit (experimental)"
}

func (r *TextToImageRule) Apply(entries []*JSONLEntry) ([]*JSONLEntry, CompactRuleReport) {
	report := CompactRuleReport{BytesBefore: entriesSize(entries)}

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

	// Render to page images
	chatHTML := buildChatHTML(toRender)
	pngPages, err := renderHTMLParallel([]string{chatHTML}, nil)
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
				"media_type": detectImageMediaType(pngData),
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
			case "image":
				sections.WriteString(renderImageBlock(b))
			case "document":
				sections.WriteString(renderDocumentBlock(b))
			case "tool_use":
				var name string
				json.Unmarshal(b["name"], &name)
				var input map[string]any
				json.Unmarshal(b["input"], &input)
				sections.WriteString(renderToolBlock("tool_use", formatToolUse(name, input)))
			case "tool_result":
				text, images := extractToolResultContent(b["content"])
				if text != "" {
					if len(text) > 3000 {
						text = truncateUTF8(text, 3000) + "\n... (truncated)"
					}
					sections.WriteString(renderToolBlock("tool_result", html.EscapeString(text)))
				}
				for _, imgHTML := range images {
					sections.WriteString(renderToolBlock("tool_result", imgHTML))
				}
			}
		}
	}
	return sections.String()
}

const chatHTMLStyle = `@page {
  size: 784px 1568px;
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
}
.bubble-img {
  margin: 2px 0;
  max-width: 95%%;
}
.bubble-img img {
  max-width: 100%%;
  height: auto;
  border-radius: 4px;
  border: 1px solid #ddd;
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

	// Build HTML for each chunk
	htmlDocs := make([]string, len(chunks))
	for i, chunk := range chunks {
		htmlDocs[i] = wrapChatHTML(buildChatSections(chunk))
	}

	// Render all chunks in parallel (pre-compiled binary, fast startup)
	allPages, err := renderHTMLParallel(htmlDocs, progressFn)
	if err != nil {
		return nil, err
	}
	return allPages, nil
}

// renderHTMLParallel renders each HTML doc in a separate process (parallel).
func renderHTMLParallel(htmlDocs []string, progressFn func(int, int)) ([][]byte, error) {
	tmpDir, err := os.MkdirTemp("", "surgery-img-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		debugDir := filepath.Join(os.TempDir(), "surgery-compact-debug")
		os.MkdirAll(debugDir, 0755)
		files, _ := os.ReadDir(tmpDir)
		for _, f := range files {
			src := filepath.Join(tmpDir, f.Name())
			dst := filepath.Join(debugDir, f.Name())
			if data, err := os.ReadFile(src); err == nil {
				os.WriteFile(dst, data, 0644)
			}
		}
	}()

	binPath, err := ensureHTML2PNGBinary(tmpDir)
	if err != nil {
		return nil, err
	}

	_, cwebpErr := exec.LookPath("cwebp")
	hasWebP := cwebpErr == nil

	type chunkResult struct {
		pages [][]byte
		err   error
	}
	results := make([]chunkResult, len(htmlDocs))

	// Launch chunks in parallel (limited concurrency to avoid overwhelming WindowServer)
	maxWorkers := 4
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	completed := 0

	for i, html := range htmlDocs {
		wg.Add(1)
		go func(idx int, htmlContent string) {
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release
			defer wg.Done()

			// Write HTML to chunk-specific file
			htmlPath := filepath.Join(tmpDir, fmt.Sprintf("chunk%d.html", idx))
			prefix := filepath.Join(tmpDir, fmt.Sprintf("chunk%d", idx))
			os.WriteFile(htmlPath, []byte(htmlContent), 0644)

			cmd := exec.Command(binPath, htmlPath, prefix)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				results[idx] = chunkResult{err: fmt.Errorf("chunk %d: %v: %s", idx, err, stderr.String())}
				return
			}

			// Collect pages
			var pages [][]byte
			singlePath := prefix + ".png"
			if _, err := os.Stat(singlePath); err == nil {
				pages = append(pages, convertPage(singlePath, hasWebP))
			} else {
				for j := 0; ; j++ {
					pagePath := fmt.Sprintf("%s-%d.png", prefix, j)
					if _, err := os.Stat(pagePath); err != nil {
						break
					}
					pages = append(pages, convertPage(pagePath, hasWebP))
				}
			}
			results[idx] = chunkResult{pages: pages}

			mu.Lock()
			completed++
			if progressFn != nil {
				progressFn(completed, len(htmlDocs))
			}
			mu.Unlock()
		}(i, html)
	}
	wg.Wait()

	var allPages [][]byte
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		allPages = append(allPages, r.pages...)
	}
	if len(allPages) == 0 {
		return nil, fmt.Errorf("no output generated")
	}
	return allPages, nil
}


func convertPage(pngPath string, hasWebP bool) []byte {
	if hasWebP {
		webpPath := strings.TrimSuffix(pngPath, ".png") + ".webp"
		cmd := exec.Command("cwebp", "-q", "95", pngPath, "-o", webpPath)
		if cmd.Run() == nil {
			if data, err := os.ReadFile(webpPath); err == nil {
				return data
			}
		}
	}
	data, _ := os.ReadFile(pngPath)
	return data
}

// ensureHTML2PNGBinary extracts the embedded pre-compiled binary to a temp path.
var html2pngBinaryPath string

func ensureHTML2PNGBinary(_ string) (string, error) {
	if html2pngBinaryPath != "" {
		if _, err := os.Stat(html2pngBinaryPath); err == nil {
			return html2pngBinaryPath, nil
		}
	}
	binPath := filepath.Join(os.TempDir(), "surgery-html2png")
	if err := os.WriteFile(binPath, html2pngBin, 0755); err != nil {
		return "", err
	}
	html2pngBinaryPath = binPath
	return binPath, nil
}

// truncateUTF8 truncates s to at most maxBytes without splitting a multi-byte character.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
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

// renderImageBlock renders an image content block as an inline <img> tag.
func renderImageBlock(b map[string]json.RawMessage) string {
	var source map[string]string
	if json.Unmarshal(b["source"], &source) != nil {
		return ""
	}
	mediaType := source["media_type"]
	data := source["data"]
	if mediaType == "" || data == "" {
		return ""
	}
	return fmt.Sprintf(`<div class="role-label">image</div><div class="bubble-img"><img src="data:%s;base64,%s"></div>`+"\n",
		html.EscapeString(mediaType), data)
}

// renderDocumentBlock renders a document content block (e.g. PDF) by converting
// its pages to images and embedding them inline.
func renderDocumentBlock(b map[string]json.RawMessage) string {
	var source map[string]string
	if json.Unmarshal(b["source"], &source) != nil {
		return ""
	}
	mediaType := source["media_type"]
	data := source["data"]
	if data == "" {
		return ""
	}

	// Decode base64 data
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return renderChatBubble("system", html.EscapeString(fmt.Sprintf("[document: %s, decode error]", mediaType)))
	}

	// Convert PDF to PNG pages
	if mediaType == "application/pdf" {
		pages, err := renderPDFToImages(raw)
		if err != nil {
			return renderChatBubble("system", html.EscapeString(fmt.Sprintf("[PDF: %d bytes, render error: %v]", len(raw), err)))
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf(`<div class="role-label">document (%d page(s))</div>`+"\n", len(pages)))
		for _, pageData := range pages {
			mt := detectImageMediaType(pageData)
			b64 := base64.StdEncoding.EncodeToString(pageData)
			sb.WriteString(fmt.Sprintf(`<div class="bubble-img"><img src="data:%s;base64,%s"></div>`+"\n",
				html.EscapeString(mt), b64))
		}
		return sb.String()
	}

	// Unknown document type — placeholder
	return renderChatBubble("system", html.EscapeString(fmt.Sprintf("[document: %s, %d bytes]", mediaType, len(raw))))
}

// renderPDFToImages converts PDF binary data to PNG page images using the embedded pdf2png binary.
func renderPDFToImages(pdfData []byte) ([][]byte, error) {
	tmpDir, err := os.MkdirTemp("", "surgery-pdf-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	binPath, err := ensurePDF2PNGBinary()
	if err != nil {
		return nil, err
	}

	pdfPath := filepath.Join(tmpDir, "input.pdf")
	if err := os.WriteFile(pdfPath, pdfData, 0644); err != nil {
		return nil, err
	}

	prefix := filepath.Join(tmpDir, "page")
	cmd := exec.Command(binPath, pdfPath, prefix)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, stderr.String())
	}

	// Collect pages (same logic as html2png output)
	_, cwebpErr := exec.LookPath("cwebp")
	hasWebP := cwebpErr == nil

	var pages [][]byte
	singlePath := prefix + ".png"
	if _, err := os.Stat(singlePath); err == nil {
		pages = append(pages, convertPage(singlePath, hasWebP))
	} else {
		for i := 0; ; i++ {
			pagePath := fmt.Sprintf("%s-%d.png", prefix, i)
			if _, err := os.Stat(pagePath); err != nil {
				break
			}
			pages = append(pages, convertPage(pagePath, hasWebP))
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no output pages generated")
	}
	return pages, nil
}

var pdf2pngBinaryPath string

func ensurePDF2PNGBinary() (string, error) {
	if pdf2pngBinaryPath != "" {
		if _, err := os.Stat(pdf2pngBinaryPath); err == nil {
			return pdf2pngBinaryPath, nil
		}
	}
	binPath := filepath.Join(os.TempDir(), "surgery-pdf2png")
	if err := os.WriteFile(binPath, pdf2pngBin, 0755); err != nil {
		return "", err
	}
	pdf2pngBinaryPath = binPath
	return binPath, nil
}

// extractToolResultContent extracts text and image HTML from a tool_result content field.
func extractToolResultContent(content json.RawMessage) (string, []string) {
	// Try as string
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s, nil
	}
	// Try as array of blocks
	var arr []json.RawMessage
	if json.Unmarshal(content, &arr) != nil {
		return "", nil
	}
	var texts []string
	var images []string
	for _, item := range arr {
		var b map[string]json.RawMessage
		if json.Unmarshal(item, &b) != nil {
			continue
		}
		var typ string
		json.Unmarshal(b["type"], &typ)
		switch typ {
		case "text":
			var t string
			json.Unmarshal(b["text"], &t)
			texts = append(texts, t)
		case "image":
			imgHTML := renderImageBlock(b)
			if imgHTML != "" {
				var source map[string]string
				json.Unmarshal(b["source"], &source)
				images = append(images, fmt.Sprintf(`<img src="data:%s;base64,%s">`,
					html.EscapeString(source["media_type"]), source["data"]))
			}
		case "document":
			docHTML := renderDocumentBlock(b)
			if docHTML != "" {
				images = append(images, docHTML)
			}
		}
	}
	return strings.Join(texts, "\n"), images
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
			cmd = truncateUTF8(cmd, 200) + "..."
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
			s = truncateUTF8(s, 200) + "..."
		}
		return html.EscapeString(fmt.Sprintf("%s(%s)", name, s))
	}
}


