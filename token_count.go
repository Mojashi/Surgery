package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image/png"
)

// messageUsage extracts the usage field from an entry's raw message JSON.
type messageUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

func (u messageUsage) TotalInputTokens() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// getLastUsage finds the usage from the last assistant entry (API-reported token count).
// Returns the usage and true if found.
func getLastUsage(entries []*JSONLEntry) (messageUsage, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type != "assistant" || e.IsSidechain {
			continue
		}
		rawMsg, ok := e.raw["message"]
		if !ok {
			continue
		}
		var msg struct {
			Usage *messageUsage `json:"usage"`
		}
		if json.Unmarshal(rawMsg, &msg) == nil && msg.Usage != nil && msg.Usage.TotalInputTokens() > 0 {
			return *msg.Usage, true
		}
	}
	return messageUsage{}, false
}

// estimateEntryTokens estimates token count for a list of entries locally.
// Images: (width * height) / 750 per Anthropic docs.
// Text: len / 4 (rough average for mixed content).
func estimateEntryTokens(entries []*JSONLEntry) int {
	total := 0
	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		var blocks []json.RawMessage
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			var s string
			if json.Unmarshal(e.Message.Content, &s) == nil {
				total += len(s) / 4
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
			case "image":
				var src struct {
					Data string `json:"data"`
				}
				json.Unmarshal(b["source"], &src)
				total += estimateBase64ImageTokens(src.Data)
			case "text":
				var text string
				json.Unmarshal(b["text"], &text)
				total += len(text) / 4
			case "tool_use":
				total += len(block) / 4
			case "tool_result":
				text := extractToolResultText(b["content"])
				total += len(text) / 4
			}
		}
	}
	return total
}

// detectImageMediaType returns the MIME type based on magic bytes.
func detectImageMediaType(data []byte) string {
	if len(data) >= 4 && string(data[:4]) == "RIFF" {
		return "image/webp"
	}
	if len(data) >= 8 && data[0] == 0x89 && string(data[1:4]) == "PNG" {
		return "image/png"
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		return "image/jpeg"
	}
	return "image/png" // fallback
}

// estimateBase64ImageTokens decodes a base64 image (PNG or WebP) to read dimensions.
func estimateBase64ImageTokens(b64data string) int {
	data, err := base64.StdEncoding.DecodeString(b64data)
	if err != nil {
		return 0
	}
	w, h := 0, 0
	// Try PNG
	if cfg, err := png.DecodeConfig(bytes.NewReader(data)); err == nil {
		w, h = cfg.Width, cfg.Height
	} else if ww, hh, ok := webpDimensions(data); ok {
		// Try WebP
		w, h = ww, hh
	} else {
		return 0
	}
	// Step 1: resize so max(w,h) <= 1568
	if w > 1568 || h > 1568 {
		scale := 1568.0 / float64(max(w, h))
		w = int(float64(w) * scale)
		h = int(float64(h) * scale)
	}
	// Step 2: ViT patches = ceil(w/28) * ceil(h/28), max 1568 patches
	pw := (w + 27) / 28
	ph := (h + 27) / 28
	if pw*ph > 1568 {
		// Shrink until patches fit
		for s := 0.99; s > 0.1; s -= 0.01 {
			npw := (int(float64(w)*s) + 27) / 28
			nph := (int(float64(h)*s) + 27) / 28
			if npw*nph <= 1568 {
				pw, ph = npw, nph
				break
			}
		}
	}
	// tokens = patches + 4 special tokens
	tokens := pw*ph + 4
	if tokens < 1 {
		return 1
	}
	return tokens
}

// webpDimensions extracts width/height from a WebP file header.
func webpDimensions(data []byte) (int, int, bool) {
	// RIFF header: "RIFF" <size> "WEBP"
	if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, false
	}
	chunk := string(data[12:16])
	switch chunk {
	case "VP8 ": // lossy
		if len(data) < 30 {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(data[26:28])) & 0x3FFF
		h := int(binary.LittleEndian.Uint16(data[28:30])) & 0x3FFF
		return w, h, true
	case "VP8L": // lossless
		if len(data) < 25 {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		w := int(bits&0x3FFF) + 1
		h := int((bits>>14)&0x3FFF) + 1
		return w, h, true
	case "VP8X": // extended
		if len(data) < 30 {
			return 0, 0, false
		}
		w := int(data[24]) | int(data[25])<<8 | int(data[26])<<16 + 1
		h := int(data[27]) | int(data[28])<<8 | int(data[29])<<16 + 1
		return w, h, true
	}
	return 0, 0, false
}
