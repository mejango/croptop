import Foundation
import Darwin

/// Per-draft atomic files and an advisory lock shared by the app and extension.
/// Submitted input is immutable, including when two processes hold stale copies.
public final class DraftStore: @unchecked Sendable {
    public let root: URL
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    public init(root: URL) throws {
        self.root = root
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        var values = URLResourceValues(); values.isExcludedFromBackup = true
        var location = root; try location.setResourceValues(values)
    }

    public func imageURL(_ draft: LocalDraft) -> URL { root.appendingPathComponent(draft.imageFilename) }
    public func previewURL(_ draft: LocalDraft) -> URL { root.appendingPathComponent("\(draft.id).preview") }

    public func create(image: Data, contentType: String, destination: SiteConnection?) throws -> LocalDraft {
        try locked { try createUnlocked(image: image, contentType: contentType, destination: destination) }
    }

    /// Only a definitive server expiry permits a fresh logical post identity.
    /// The old draft/receipt remains available, including its original ID.
    public func startNewAfterRejection(_ id: String) throws -> LocalDraft {
        try locked {
            let old = try read(id)
            guard old.canStartCorrectedDraft else { throw MobileError.invalid("Check the existing post's status before starting again.") }
            var replacement = try createUnlocked(image: Data(contentsOf: imageURL(old)), contentType: old.contentType, destination: old.destination)
            replacement.title = old.title; replacement.caption = old.caption
            try write(replacement); return replacement
        }
    }

    public func list() throws -> [LocalDraft] {
        try locked {
            try FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil)
                .filter { $0.pathExtension == "json" && $0.lastPathComponent != "connection.json" }
                .map { try decoder.decode(LocalDraft.self, from: Data(contentsOf: $0)) }
                .sorted { $0.createdAt > $1.createdAt }
        }
    }

    public func load(_ id: String) throws -> LocalDraft {
        try locked { try read(id) }
    }

    @discardableResult
    public func edit(_ id: String, title: String, caption: String, destination: SiteConnection?) throws -> LocalDraft {
        try locked {
            var draft = try read(id)
            guard !draft.submitted else { throw MobileError.invalid("This post is already pending. Its image, text and destination are preserved for recovery.") }
            if let old = draft.destination, let destination, old.ipns != destination.ipns || old.origin != destination.origin {
                throw MobileError.invalid("This draft belongs to another site. Reconnect that site to publish it.")
            }
            draft.title = title; draft.caption = caption
            if draft.destination == nil { draft.destination = destination }
            try write(draft); return draft
        }
    }

    @discardableResult
    public func beginSubmission(_ id: String) throws -> LocalDraft {
        try beginSubmissionAttempt(id).draft
    }

    public func beginSubmissionAttempt(_ id: String, validate: ((LocalDraft, Data) throws -> Void)? = nil) throws -> (draft: LocalDraft, wasSubmitted: Bool, image: Data) {
        try locked {
            var draft = try read(id)
            guard draft.destination != nil else { throw MobileError.invalid("Choose a destination before publishing.") }
            let image = try Data(contentsOf: imageURL(draft))
            try validate?(draft, image)
            let wasSubmitted = draft.submitted
            draft.submitted = true; draft.lastError = nil; draft.lastErrorCode = nil
            try write(draft); return (draft, wasSubmitted, image)
        }
    }

    public func retainRejectedLocalDraft(_ id: String) throws {
        try locked {
            var draft = try read(id)
            guard draft.operation == nil, draft.commitAttempted != true else { return }
            draft.submitted = false; try write(draft)
        }
    }

    public func replaceImage(_ image: Data, contentType: String, for id: String) throws {
        try locked {
            var draft = try read(id)
            guard !draft.submitted else { throw MobileError.invalid("The submitted image is retained for recovery. Check its status first.") }
            try protectedWrite(image, to: imageURL(draft))
            draft.contentType = contentType; try write(draft)
        }
    }

    public func beginCommit(_ id: String, proposalID: String) throws {
        try locked {
            var draft = try read(id)
            guard draft.operation?.state == .needsSignature, draft.operation?.proposal?.id == proposalID else {
                throw MobileError.invalid("Refresh this post's status before publishing.")
            }
            draft.commitAttempted = true; draft.hasEverAttemptedCommit = true; try write(draft)
        }
    }

    @discardableResult
    public func record(_ operation: MobileOperation, for id: String) throws -> LocalDraft {
        try locked {
            var draft = try read(id)
            guard operation.id == draft.id, operation.postID == draft.id,
                  operation.ipns == draft.destination?.ipns,
                  operation.title == draft.title, operation.caption == draft.caption else {
                throw MobileError.invalid("The service returned a different post. Nothing was signed.")
            }
            if operation.state == .published {
                guard let rawURL = operation.url, let url = URL(string: rawURL), url.scheme == "https", url.host != nil else {
                    throw MobileError.invalid("Publication is not confirmed with a valid post link yet.")
                }
            }
            // A late network result cannot move a confirmed post backwards.
            if draft.operation?.state != .published {
                draft.operation = operation; draft.lastError = nil; draft.lastErrorCode = nil
                if operation.proposal != nil { draft.hasEverReceivedProposal = true }
                draft.commitAttempted = false; try write(draft)
            }
            return draft
        }
    }

    public func noteError(_ error: String, code: String? = nil, for id: String) throws {
        try locked {
            var draft = try read(id); draft.lastError = error; draft.lastErrorCode = code; try write(draft)
        }
    }

    public func savePreview(_ data: Data, for draft: LocalDraft) throws { try locked { try protectedWrite(data, to: previewURL(draft)) } }

    public func connection() throws -> SiteConnection? {
        try locked {
            let url = root.appendingPathComponent("connection.json")
            guard FileManager.default.fileExists(atPath: url.path) else { return nil }
            return try decoder.decode(SiteConnection.self, from: Data(contentsOf: url))
        }
    }

    public func setConnection(_ connection: SiteConnection?) throws {
        try locked {
            let url = root.appendingPathComponent("connection.json")
            if let connection { try protectedWrite(encoder.encode(connection), to: url) }
            else if FileManager.default.fileExists(atPath: url.path) { try FileManager.default.removeItem(at: url) }
        }
    }

    private func read(_ id: String) throws -> LocalDraft {
        guard UUID(uuidString: id)?.uuidString == id else { throw MobileError.storage("Invalid saved draft identifier.") }
        return try decoder.decode(LocalDraft.self, from: Data(contentsOf: root.appendingPathComponent("\(id).json")))
    }

    private func write(_ draft: LocalDraft) throws { try protectedWrite(encoder.encode(draft), to: root.appendingPathComponent("\(draft.id).json")) }

    private func createUnlocked(image: Data, contentType: String, destination: SiteConnection?) throws -> LocalDraft {
        let id = UUID().uuidString
        let draft = LocalDraft(id: id, imageFilename: "\(id).image", contentType: contentType, destination: destination)
        try protectedWrite(image, to: imageURL(draft))
        do { try write(draft) } catch { try? FileManager.default.removeItem(at: imageURL(draft)); throw error }
        return draft
    }

    private func protectedWrite(_ data: Data, to url: URL) throws {
        #if os(iOS)
        try data.write(to: url, options: [.atomic, .completeFileProtection])
        #else
        try data.write(to: url, options: .atomic)
        #endif
    }

    private func locked<T>(_ body: () throws -> T) throws -> T {
        let descriptor = open(root.appendingPathComponent(".lock").path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard descriptor >= 0 else { throw MobileError.storage("Draft storage cannot be opened.") }
        defer { close(descriptor) }
        guard flock(descriptor, LOCK_EX) == 0 else { throw MobileError.storage("Draft storage is busy. Try again.") }
        defer { flock(descriptor, LOCK_UN) }
        return try body()
    }
}
