#!/usr/bin/env swift
// pdf2png.swift — Render PDF pages to PNG images using PDFKit
//
// Usage: pdf2png <input.pdf> <output-prefix> [max-width]
//   Outputs: prefix-0.png, prefix-1.png, ... (or prefix.png if single page)

import Cocoa
import PDFKit

let maxWidthDefault = 784

guard CommandLine.arguments.count >= 3 else {
    fputs("Usage: pdf2png <input.pdf> <output-prefix> [max-width]\n", stderr)
    exit(1)
}

let pdfPath = CommandLine.arguments[1]
let outputPrefix = CommandLine.arguments[2]
let maxWidth = CommandLine.arguments.count >= 4 ? Int(CommandLine.arguments[3]) ?? maxWidthDefault : maxWidthDefault

guard let pdfDoc = PDFDocument(url: URL(fileURLWithPath: pdfPath)) else {
    fputs("Error: cannot open PDF \(pdfPath)\n", stderr)
    exit(1)
}

let pageCount = pdfDoc.pageCount
if pageCount == 0 {
    fputs("Error: PDF has no pages\n", stderr)
    exit(1)
}

for i in 0..<pageCount {
    guard let page = pdfDoc.page(at: i) else { continue }

    let mediaBox = page.bounds(for: .mediaBox)
    let scale = min(CGFloat(maxWidth) / mediaBox.width, 3.0) // cap at 3x to avoid huge images
    let w = Int(mediaBox.width * scale)
    let h = Int(mediaBox.height * scale)

    let bitmapRep = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: w, pixelsHigh: h,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    bitmapRep.size = NSSize(width: w, height: h)

    NSGraphicsContext.saveGraphicsState()
    let ctx = NSGraphicsContext(bitmapImageRep: bitmapRep)!
    NSGraphicsContext.current = ctx

    // White background
    NSColor.white.setFill()
    NSRect(x: 0, y: 0, width: w, height: h).fill()

    // Scale and draw PDF page
    let cgCtx = ctx.cgContext
    cgCtx.scaleBy(x: scale, y: scale)
    page.draw(with: .mediaBox, to: cgCtx)

    NSGraphicsContext.restoreGraphicsState()

    guard let pngData = bitmapRep.representation(using: .png, properties: [:]) else {
        fputs("Error: failed to create PNG for page \(i)\n", stderr)
        continue
    }

    let filename = pageCount == 1
        ? "\(outputPrefix).png"
        : "\(outputPrefix)-\(i).png"
    try? pngData.write(to: URL(fileURLWithPath: filename))
}

fputs("Rendered \(pageCount) page(s)\n", stderr)
