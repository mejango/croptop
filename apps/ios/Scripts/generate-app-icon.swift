#!/usr/bin/env swift
// Run from any directory: swift apps/ios/Scripts/generate-app-icon.swift [--check]
// The mobile SVG owns the artwork; iOS supplies the final rounded icon mask.
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

struct IconError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

func require(_ condition: Bool, _ message: String) throws {
    if !condition { throw IconError(message: message) }
}

func number(_ value: String?) throws -> CGFloat {
    guard let value, let parsed = Double(value), parsed.isFinite else {
        throw IconError(message: "Invalid or missing SVG number: \(value ?? "nil")")
    }
    return CGFloat(parsed)
}

func color(_ value: String) throws -> CGColor? {
    if value == "none" { return nil }
    try require(value.count == 7 && value.first == "#", "Expected an opaque #RRGGBB SVG color")
    guard let rgb = UInt32(value.dropFirst(), radix: 16) else {
        throw IconError(message: "Invalid SVG color: \(value)")
    }
    return CGColor(colorSpace: CGColorSpace(name: CGColorSpace.sRGB)!, components: [
        CGFloat((rgb >> 16) & 255) / 255,
        CGFloat((rgb >> 8) & 255) / 255,
        CGFloat(rgb & 255) / 255,
        1,
    ])!
}

// Fail on unsupported SVG features instead of silently generating different artwork.
final class IconSVG: NSObject, XMLParserDelegate {
    var viewBox: CGRect?
    var shapes: [(name: String, attributes: [String: String])] = []
    var failure: Error?

    func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?,
                qualifiedName: String?, attributes: [String: String]) {
        do {
            let allowed: Set<String>
            switch name {
            case "svg":
                allowed = ["xmlns", "viewBox"]
                try require(viewBox == nil, "Only one SVG root is supported")
                let values = try (attributes["viewBox"] ?? "").split(whereSeparator: { $0.isWhitespace || $0 == "," })
                    .map { try number(String($0)) }
                try require(values.count == 4 && values[2] > 0 && values[3] > 0, "Invalid SVG viewBox")
                viewBox = CGRect(x: values[0], y: values[1], width: values[2], height: values[3])
            case "rect":
                allowed = ["x", "y", "width", "height", "rx", "fill"]
                shapes.append((name, attributes))
            case "path":
                allowed = ["d", "fill", "stroke", "stroke-width"]
                shapes.append((name, attributes))
            default:
                throw IconError(message: "Unsupported SVG element: \(name)")
            }
            try require(Set(attributes.keys).isSubset(of: allowed), "Unsupported attributes on SVG \(name)")
        } catch {
            failure = error
            parser.abortParsing()
        }
    }
}

func path(_ source: String) throws -> CGPath {
    let expression = try NSRegularExpression(pattern: #"[A-Za-z]|[-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?"#)
    let range = NSRange(source.startIndex..., in: source)
    let matches = expression.matches(in: source, range: range)
    let residue = expression.stringByReplacingMatches(in: source, range: range, withTemplate: "")
    try require(residue.allSatisfy { $0.isWhitespace || $0 == "," }, "Invalid SVG path syntax")
    let tokens = matches.map { String(source[Range($0.range, in: source)!]) }
    var index = 0
    var command: String?
    var point = CGPoint.zero
    var start = CGPoint.zero
    let result = CGMutablePath()
    func next() throws -> CGFloat {
        try require(index < tokens.count, "Incomplete SVG path command")
        defer { index += 1 }
        return try number(tokens[index])
    }
    while index < tokens.count {
        if tokens[index].first!.isLetter {
            command = tokens[index]
            index += 1
        }
        guard let active = command else { throw IconError(message: "Missing SVG path command") }
        switch active {
        case "M", "m", "L", "l":
            let x = try next()
            let y = try next()
            let relative = active == active.lowercased()
            point = CGPoint(x: x + (relative ? point.x : 0), y: y + (relative ? point.y : 0))
            if active.uppercased() == "M" {
                result.move(to: point)
                start = point
                command = relative ? "l" : "L"
            } else {
                result.addLine(to: point)
            }
        case "H", "h":
            point.x = try next() + (active == "h" ? point.x : 0)
            result.addLine(to: point)
        case "V", "v":
            point.y = try next() + (active == "v" ? point.y : 0)
            result.addLine(to: point)
        case "Z", "z":
            result.closeSubpath()
            point = start
            command = nil
        default:
            throw IconError(message: "Unsupported SVG path command: \(active)")
        }
    }
    return result
}

do {
    try require(CommandLine.arguments.dropFirst().allSatisfy { $0 == "--check" }, "Usage: generate-app-icon.swift [--check]")
    let iosDirectory = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
    let svgURL = iosDirectory.appendingPathComponent("../../web/mobile/icon.svg").standardizedFileURL
    let iconURL = iosDirectory.appendingPathComponent("Assets.xcassets/AppIcon.appiconset/AppIcon.png")
    let parser = XMLParser(data: try Data(contentsOf: svgURL))
    let svg = IconSVG()
    parser.delegate = svg
    let parsed = parser.parse()
    if let failure = svg.failure { throw failure }
    try require(parsed, "Could not parse mobile icon SVG")
    guard let viewBox = svg.viewBox, let background = svg.shapes.first,
          background.name == "rect", let backgroundFill = background.attributes["fill"],
          let backgroundColor = try color(backgroundFill) else {
        throw IconError(message: "Mobile icon must start with an opaque background rectangle")
    }
    try require(viewBox.width == viewBox.height, "App icon SVG must be square")
    let size = 1024
    guard let context = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8,
                                  bytesPerRow: size * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                  bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue) else {
        throw IconError(message: "Could not create app icon context")
    }
    context.setFillColor(backgroundColor)
    context.fill(CGRect(x: 0, y: 0, width: size, height: size))
    context.translateBy(x: 0, y: CGFloat(size))
    context.scaleBy(x: CGFloat(size) / viewBox.width, y: -CGFloat(size) / viewBox.height)
    context.translateBy(x: -viewBox.minX, y: -viewBox.minY)
    context.setLineCap(.butt)
    context.setLineJoin(.miter)
    context.setMiterLimit(4)
    for shape in svg.shapes {
        let attributes = shape.attributes
        let geometry: CGPath
        if shape.name == "rect" {
            let rect = try CGRect(x: number(attributes["x"] ?? "0"), y: number(attributes["y"] ?? "0"),
                                  width: number(attributes["width"]), height: number(attributes["height"]))
            let radius = try number(attributes["rx"] ?? "0")
            geometry = CGPath(roundedRect: rect, cornerWidth: radius, cornerHeight: radius, transform: nil)
        } else {
            geometry = try path(attributes["d"] ?? "")
        }
        if let fill = try color(attributes["fill"] ?? "#000000") {
            context.addPath(geometry)
            context.setFillColor(fill)
            context.fillPath()
        }
        if let stroke = try color(attributes["stroke"] ?? "none") {
            context.addPath(geometry)
            context.setStrokeColor(stroke)
            context.setLineWidth(try number(attributes["stroke-width"] ?? "1"))
            context.strokePath()
        }
    }
    guard let image = context.makeImage(), let data = CFDataCreateMutable(nil, 0),
          let destination = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil) else {
        throw IconError(message: "Could not create app icon PNG")
    }
    CGImageDestinationAddImage(destination, image, nil)
    try require(CGImageDestinationFinalize(destination), "Could not encode app icon PNG")
    if CommandLine.arguments.contains("--check") {
        try require(try Data(contentsOf: iconURL) == data as Data, "AppIcon.png is stale; rerun generate-app-icon.swift")
        print("AppIcon.png matches web/mobile/icon.svg (1024 × 1024, opaque sRGB).")
    } else {
        try FileManager.default.createDirectory(at: iconURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        try (data as Data).write(to: iconURL, options: .atomic)
        print("Generated \(iconURL.path) (1024 × 1024, opaque sRGB).")
    }
} catch {
    fputs("App icon generation failed: \(error.localizedDescription)\n", stderr)
    exit(1)
}
