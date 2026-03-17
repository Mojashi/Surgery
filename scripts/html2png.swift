#!/usr/bin/env swift
// html2png.swift — Render multiple HTML files to PNG pages using WKWebView
// Splits at element boundaries to avoid cutting text.
//
// Batch mode: html2png <output-dir> [page-height]
//   Reads JSON from stdin: [{"html": "...", "prefix": "chunk0"}, ...]
//   Writes: output-dir/chunk0-0.png, chunk0-1.png, ...
//
// Single mode: html2png <input.html> <output-prefix> [page-height]

import Cocoa
import WebKit

let viewWidth = 784
let defaultPageHeight = 1568

// Parse args
var batchMode = false
var outputDir = ""
var singleHTML = ""
var singlePrefix = ""
var pageHeight = defaultPageHeight

if CommandLine.arguments.count >= 2 {
    let arg1 = CommandLine.arguments[1]
    if arg1.hasSuffix(".html") {
        // Single mode
        singleHTML = arg1
        singlePrefix = CommandLine.arguments.count >= 3 ? CommandLine.arguments[2] : "/tmp/output"
        pageHeight = CommandLine.arguments.count >= 4 ? Int(CommandLine.arguments[3]) ?? defaultPageHeight : defaultPageHeight
    } else {
        // Batch mode: arg1 = output dir
        batchMode = true
        outputDir = arg1
        pageHeight = CommandLine.arguments.count >= 3 ? Int(CommandLine.arguments[2]) ?? defaultPageHeight : defaultPageHeight
    }
}

struct Job {
    let html: String
    let prefix: String
}

var jobs: [Job] = []

if batchMode {
    // Read JSON array from stdin
    let input = FileHandle.standardInput.readDataToEndOfFile()
    if let arr = try? JSONSerialization.jsonObject(with: input) as? [[String: String]] {
        for item in arr {
            if let html = item["html"], let prefix = item["prefix"] {
                jobs.append(Job(html: html, prefix: "\(outputDir)/\(prefix)"))
            }
        }
    }
    if jobs.isEmpty {
        fputs("Error: no jobs in stdin\n", stderr)
        exit(1)
    }
} else {
    guard let html = try? String(contentsOfFile: singleHTML, encoding: .utf8) else {
        fputs("Error: cannot read \(singleHTML)\n", stderr)
        exit(1)
    }
    jobs.append(Job(html: html, prefix: singlePrefix))
}

class Renderer: NSObject, WKNavigationDelegate {
    let webView: WKWebView
    let pageHeight: Int
    var jobs: [Job]
    var currentJob: Int = 0
    var done = false
    var totalPages = 0

    init(pageHeight: Int, width: Int, jobs: [Job]) {
        self.pageHeight = pageHeight
        self.jobs = jobs
        let config = WKWebViewConfiguration()
        self.webView = WKWebView(frame: NSRect(x: 0, y: 0, width: width, height: pageHeight), configuration: config)
        super.init()
        self.webView.navigationDelegate = self
    }

    func start() {
        loadNext()
    }

    func loadNext() {
        if currentJob >= jobs.count {
            fputs("Total: \(totalPages) pages from \(jobs.count) jobs\n", stderr)
            done = true
            return
        }
        webView.loadHTMLString(jobs[currentJob].html, baseURL: nil)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
            self.computePageBreaks()
        }
    }

    func computePageBreaks() {
        let js = """
        (function() {
            var maxH = \(pageHeight);
            var breaks = [0];
            var children = document.body.children;
            var y = 0;
            for (var i = 0; i < children.length; i++) {
                var rect = children[i].getBoundingClientRect();
                var bottom = rect.bottom + window.scrollY;
                if (bottom - breaks[breaks.length - 1] > maxH && y > breaks[breaks.length - 1]) {
                    breaks.push(y);
                }
                y = bottom;
            }
            breaks.push(document.body.scrollHeight);
            return JSON.stringify(breaks);
        })()
        """
        webView.evaluateJavaScript(js) { [self] result, error in
            guard let jsonStr = result as? String,
                  let data = jsonStr.data(using: .utf8),
                  let breaks = try? JSONSerialization.jsonObject(with: data) as? [Int],
                  breaks.count >= 2 else {
                fputs("Error computing page breaks for job \(currentJob)\n", stderr)
                currentJob += 1
                loadNext()
                return
            }

            var pages: [(Int, Int)] = []
            for i in 0..<breaks.count - 1 {
                let start = breaks[i]
                let end = breaks[i + 1]
                if end > start { pages.append((start, end - start)) }
            }

            let prefix = jobs[currentJob].prefix
            var captured = 0

            func captureNext() {
                if captured >= pages.count {
                    totalPages += captured
                    currentJob += 1
                    loadNext()
                    return
                }

                let (y, h) = pages[captured]
                webView.frame = NSRect(x: 0, y: 0, width: Int(webView.frame.width), height: h)
                webView.evaluateJavaScript("window.scrollTo(0, \(y))") { _, _ in
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) {
                        let config = WKSnapshotConfiguration()
                        config.rect = NSRect(x: 0, y: 0, width: Int(self.webView.frame.width), height: h)

                        self.webView.takeSnapshot(with: config) { image, error in
                            guard let image = image else {
                                fputs("Error capturing \(prefix) page \(captured)\n", stderr)
                                captured += 1
                                captureNext()
                                return
                            }

                            let targetW = Int(self.webView.frame.width)
                            let targetH = h
                            let bitmapRep = NSBitmapImageRep(
                                bitmapDataPlanes: nil, pixelsWide: targetW, pixelsHigh: targetH,
                                bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
                            bitmapRep.size = NSSize(width: targetW, height: targetH)
                            NSGraphicsContext.saveGraphicsState()
                            NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmapRep)
                            image.draw(in: NSRect(x: 0, y: 0, width: targetW, height: targetH),
                                       from: NSRect(origin: .zero, size: image.size),
                                       operation: .copy, fraction: 1.0)
                            NSGraphicsContext.restoreGraphicsState()

                            let pngData = bitmapRep.representation(using: .png, properties: [:])!
                            let filename = pages.count == 1
                                ? "\(prefix).png"
                                : "\(prefix)-\(captured).png"

                            try? pngData.write(to: URL(fileURLWithPath: filename))

                            captured += 1
                            captureNext()
                        }
                    }
                }
            }

            captureNext()
        }
    }
}

let app = NSApplication.shared
let renderer = Renderer(pageHeight: pageHeight, width: viewWidth, jobs: jobs)
renderer.start()

while !renderer.done {
    RunLoop.current.run(mode: .default, before: Date(timeIntervalSinceNow: 0.05))
}
