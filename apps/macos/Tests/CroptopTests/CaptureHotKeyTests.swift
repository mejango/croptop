import XCTest
import Carbon
@testable import Croptop

@MainActor
final class CaptureHotKeyTests: XCTestCase {
    func testRegistersDispatchesOnlyOwnEventsAndUnregisters() async throws {
        // An unused chord tests Carbon without changing the user's screenshot binding.
        let key = UInt32(kVK_F19), modifiers = UInt32(cmdKey | shiftKey | controlKey | optionKey)
        var presses = 0
        var hotKey: CaptureHotKey? = try CaptureHotKey(keyCode: key, modifiers: modifiers) { presses += 1 }
        XCTAssertNotNil(hotKey)
        try send(id: 999)
        try await Task.sleep(nanoseconds: 20_000_000)
        XCTAssertEqual(presses, 0)
        try send(id: 1)
        try await Task.sleep(nanoseconds: 20_000_000)
        XCTAssertEqual(presses, 1)
        XCTAssertThrowsError(try CaptureHotKey(keyCode: key, modifiers: modifiers) {})
        hotKey = nil
        let replacement = try CaptureHotKey(keyCode: key, modifiers: modifiers) {}
        XCTAssertNotNil(replacement)
    }

    private func send(id: UInt32) throws {
        var event: EventRef?
        XCTAssertEqual(CreateEvent(nil, OSType(kEventClassKeyboard), UInt32(kEventHotKeyPressed), 0, 0, &event), noErr)
        let value = try XCTUnwrap(event)
        defer { ReleaseEvent(value) }
        var identifier = EventHotKeyID(signature: 0x43524F50, id: id)
        XCTAssertEqual(SetEventParameter(value, EventParamName(kEventParamDirectObject), EventParamType(typeEventHotKeyID),
                                        MemoryLayout<EventHotKeyID>.size, &identifier), noErr)
        _ = SendEventToEventTarget(value, GetApplicationEventTarget())
    }
}
