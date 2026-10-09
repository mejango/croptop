import AppKit
import SwiftUI
import Vision
import XCTest
@testable import Croptop

final class SiteHostingToggleTests: XCTestCase {
    @MainActor private func host(isOn: Bool, changed: Bool, busy: Bool = false, canSave: Bool = true,
                                 save: @escaping () -> Void = {}) -> NSHostingView<AnyView> {
        _ = NSApplication.shared
        AppFonts.register()
        let content = SiteHostingToggle(isOn: .constant(isOn), changed: changed, busy: busy, canSave: canSave, save: save)
            .padding(20).frame(width: 600, height: 180, alignment: .topLeading)
            .foregroundColor(Theme.ink).background(Theme.paper)
        let view = NSHostingView(rootView: AnyView(content))
        view.frame = NSRect(x: 0, y: 0, width: 600, height: 180)
        view.layoutSubtreeIfNeeded()
        return view
    }

    @MainActor private func bitmap(_ view: NSView) throws -> NSBitmapImageRep {
        let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
        view.cacheDisplay(in: view.bounds, to: bitmap)
        return bitmap
    }

    @MainActor func testRenderedCheckboxHasConciseHelpAndOnlyChangedChoicesOfferSave() throws {
        for isOn in [false, true] {
            for changed in [false, true] {
                let view = host(isOn: isOn, changed: changed)
                let rendered = try bitmap(view)
                let request = VNRecognizeTextRequest()
                request.recognitionLevel = .accurate
                request.recognitionLanguages = ["en-US"]
                try VNImageRequestHandler(cgImage: XCTUnwrap(rendered.cgImage)).perform([request])
                let lines = (request.results ?? []).compactMap { $0.topCandidates(1).first?.string }
                let text = lines.joined(separator: " ")
                XCTAssertTrue(text.contains("Use crop.top"), text)
                XCTAssertTrue(text.contains("reliable, fast peer"), text)
                XCTAssertTrue(text.contains("when this computer is offline"), text)
                XCTAssertFalse(text.contains("Save and publish to send your content there"), text)
                XCTAssertFalse(text.contains("Publishing host"), text)
                XCTAssertEqual(lines.contains("Save and publish"), changed, text)

                if let directory = ProcessInfo.processInfo.environment["CROPTOP_HOSTING_TOGGLE_SNAPSHOT_DIR"] {
                    try FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
                    let data = try XCTUnwrap(rendered.representation(using: .png, properties: [:]))
                    let name = "\(isOn ? "on" : "off")-\(changed ? "changed" : "saved").png"
                    try data.write(to: URL(fileURLWithPath: directory).appendingPathComponent(name))
                }
            }
        }
    }

}
