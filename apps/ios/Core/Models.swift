import Foundation

public struct SiteConnection: Codable, Equatable, Sendable {
    public let origin: URL
    public let ipns: String
    public var name: String
    public var url: String
    public var enabled: Bool

    public init(origin: URL, ipns: String, name: String, url: String = "", enabled: Bool = false) {
        self.origin = origin; self.ipns = ipns; self.name = name; self.url = url; self.enabled = enabled
    }
}

public struct ServiceConfiguration: Decodable, Sendable {
    public let version: Int
    public let enabled: Bool
    public let origin: String
    public let host: String
    public let maxImageBytes: Int
    public let maxImagePixels: Int
    public let maxTitleBytes: Int
    public let maxCaptionBytes: Int
    public let formats: [String]
    public let maxImages: Int
}

public struct SiteStatus: Decodable, Sendable {
    public let ipns: String
    public let name: String
    public let url: String
    public let ready: Bool
    public let enabled: Bool
    public let reason: String?
}

public struct MobileProposal: Codable, Equatable, Sendable {
    public let id: String
    public let cid: String
    public let parent: String
    public let sequence: String
    public let host: String
    public let time: Int64
    public let expiresAt: Int64
    public let recordPayload: String
    public let pushPayload: String
}

public enum OperationState: String, Codable, Sendable {
    case preparing, needsSignature = "needs_signature", committing, published, failed
}

public struct MobileOperation: Codable, Equatable, Sendable {
    public let id: String
    public let ipns: String
    public let postID: String
    public let state: OperationState
    public let title: String
    public let caption: String
    public let createdAt: Int64
    public let mediaSHA256: String
    public let mediaType: String
    public let proposal: MobileProposal?
    public let url: String?
    public let error: String?
    public let code: String?
}

public struct LocalDraft: Codable, Identifiable, Equatable, Sendable {
    public let id: String
    public let createdAt: Date
    public let imageFilename: String
    public var contentType: String
    public var title: String
    public var caption: String
    // Once assigned, a draft never silently follows a replacement connection.
    public var destination: SiteConnection?
    // Persist before POST. A lost response must preserve the exact upload body.
    public var submitted: Bool
    public var operation: MobileOperation?
    public var lastError: String?
    public var lastErrorCode: String?
    public var commitAttempted: Bool?
    public var hasEverAttemptedCommit: Bool?
    public var hasEverReceivedProposal: Bool?

    public var commitIsUnconfirmed: Bool { commitAttempted == true && operation?.state != .published }

    public var canStartAgainAfterExpiry: Bool {
        operation?.state != .published && (operation?.code == "draft_expired" || lastErrorCode == "draft_expired")
    }

    public var canStartCorrectedDraft: Bool {
        canStartAgainAfterExpiry || (operation?.state == .failed && ["image_invalid", "heif_unavailable"].contains(operation?.code ?? "") &&
            hasEverAttemptedCommit != true && hasEverReceivedProposal != true && operation?.proposal == nil)
    }

    public init(id: String = UUID().uuidString, imageFilename: String, contentType: String, destination: SiteConnection?) {
        self.id = id; createdAt = Date(); self.imageFilename = imageFilename
        self.contentType = contentType; self.destination = destination
        title = ""; caption = ""; submitted = false
    }

    public var statusLabel: String {
        if commitIsUnconfirmed { return "Confirming publication" }
        switch operation?.state {
        case .published: return "Published"
        case .needsSignature: return "Preview ready"
        case .committing: return "Confirming publication"
        case .preparing: return "Preparing"
        case .failed: return "Needs attention"
        case nil: return submitted ? "Pending — check status" : "Draft"
        }
    }
}

public enum MobileError: LocalizedError {
    case invalid(String)
    case service(Int, String, String)
    case storage(String)

    public var errorDescription: String? {
        switch self {
        case .invalid(let message), .storage(let message): return message
        case .service(_, _, let message): return message
        }
    }
}
