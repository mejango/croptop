import AppKit
import Carbon

@MainActor
final class CaptureHotKey {
    private var reference: EventHotKeyRef?
    private var handler: EventHandlerRef?
    let action: () -> Void

    init(keyCode: UInt32 = UInt32(kVK_ANSI_C), modifiers: UInt32 = UInt32(cmdKey | shiftKey), action: @escaping () -> Void) throws {
        self.action = action
        var event = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        let installed = InstallEventHandler(GetApplicationEventTarget(), { _, event, context in
            guard let context, let event else { return OSStatus(eventNotHandledErr) }
            var id = EventHotKeyID()
            guard GetEventParameter(event, EventParamName(kEventParamDirectObject), EventParamType(typeEventHotKeyID), nil,
                                    MemoryLayout<EventHotKeyID>.size, nil, &id) == noErr,
                  id.signature == 0x43524F50, id.id == 1 else { return OSStatus(eventNotHandledErr) }
            let hotKey = Unmanaged<CaptureHotKey>.fromOpaque(context).takeUnretainedValue()
            Task { @MainActor in hotKey.action() }
            return noErr
        }, 1, &event, Unmanaged.passUnretained(self).toOpaque(), &handler)
        guard installed == noErr else { throw CaptureError.shortcut }
        let id = EventHotKeyID(signature: 0x43524F50, id: 1) // CROP
        let registered = RegisterEventHotKey(keyCode, modifiers, id,
                                            GetApplicationEventTarget(), 0, &reference)
        guard registered == noErr else {
            if let handler { RemoveEventHandler(handler); self.handler = nil }
            throw CaptureError.shortcut
        }
    }
    deinit {
        if let reference { UnregisterEventHotKey(reference) }
        if let handler { RemoveEventHandler(handler) }
    }
}

enum CaptureError: LocalizedError {
    case shortcut
    case failed(String)
    var errorDescription: String? {
        switch self {
        case .shortcut: return "Couldn’t enable ⌘⇧C. Check whether another app is using that shortcut."
        case .failed(let message): return message.isEmpty ? "Couldn’t take a screenshot." : message
        }
    }
}

enum ScreenshotCapture {
    /// A canceled region selection produces no file and no post.
    static func capture(to destination: URL) async throws -> Bool {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
        process.arguments = ["-i", "-x", "-t", "png", destination.path]
        let errors = Pipe(); process.standardError = errors
        let status: Int32 = try await withCheckedThrowingContinuation { continuation in
            process.terminationHandler = { continuation.resume(returning: $0.terminationStatus) }
            do { try process.run() } catch { continuation.resume(throwing: error) }
        }
        let exists = FileManager.default.fileExists(atPath: destination.path)
        if status == 0 && exists { return true }
        let message = String(data: errors.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if !message.isEmpty { throw CaptureError.failed(message) }
        return false
    }
}
