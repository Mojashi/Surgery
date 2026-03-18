.PHONY: build clean

# Pre-compiled Swift binaries, embedded via go:embed
HTML2PNG_BIN = scripts/html2png
HTML2PNG_SRC = scripts/html2png.swift
PDF2PNG_BIN = scripts/pdf2png
PDF2PNG_SRC = scripts/pdf2png.swift

$(HTML2PNG_BIN): $(HTML2PNG_SRC)
	swiftc $< -o $@ -framework WebKit -framework Cocoa -O
	@echo "Compiled html2png binary"

$(PDF2PNG_BIN): $(PDF2PNG_SRC)
	swiftc $< -o $@ -framework Cocoa -framework PDFKit -O
	@echo "Compiled pdf2png binary"

build: $(HTML2PNG_BIN) $(PDF2PNG_BIN)
	wails build -platform darwin/arm64 -ldflags "-X main.Version=dev"

release: $(HTML2PNG_BIN) $(PDF2PNG_BIN)
	wails build -platform darwin/arm64 -ldflags "-X main.Version=$(VERSION)"

clean:
	rm -f $(HTML2PNG_BIN) $(PDF2PNG_BIN)
