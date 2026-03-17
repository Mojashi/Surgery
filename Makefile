.PHONY: build clean

# Pre-compiled Swift binary, embedded via go:embed
SWIFT_BIN = scripts/html2png
SWIFT_SRC = scripts/html2png.swift

$(SWIFT_BIN): $(SWIFT_SRC)
	swiftc $< -o $@ -framework WebKit -framework Cocoa -O
	@echo "Compiled html2png binary"

build: $(SWIFT_BIN)
	wails build -platform darwin/arm64 -ldflags "-X main.Version=dev"

release: $(SWIFT_BIN)
	wails build -platform darwin/arm64 -ldflags "-X main.Version=$(VERSION)"

clean:
	rm -f $(SWIFT_BIN)
