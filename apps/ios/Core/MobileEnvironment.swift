import Foundation

public enum MobileEnvironment {
    /// Packaged with the core; the project generator derives associated domains
    /// from this same resource. There is no historical-origin fallback.
    public static let productionOrigin: URL = {
        struct Configuration: Decodable { let origin: String }
        guard let location = Bundle.module.url(forResource: "Service", withExtension: "json"),
              let data = try? Data(contentsOf: location),
              let config = try? JSONDecoder().decode(Configuration.self, from: data),
              let url = URL(string: config.origin), url.scheme == "https",
              (try? SessionChallengeValidator.validatedOrigin(url)) == config.origin else {
            preconditionFailure("The packaged mobile service configuration is invalid.")
        }
        return url
    }()

    public static func requireApprovedOrigin(_ origin: URL) throws {
        guard origin.absoluteString == productionOrigin.absoluteString else {
            throw MobileError.invalid("This saved connection uses an unapproved publishing service. Remove it and connect again with an updated publisher. Your drafts are retained.")
        }
    }
}

public struct PairingLink: CustomStringConvertible, CustomDebugStringConvertible, Sendable {
    public let origin: URL
    let id: String
    let capability: String
    public var description: String { "Croptop connection link [redacted]" }
    public var debugDescription: String { description }

    public init(_ raw: String) throws {
        let origin = MobileEnvironment.productionOrigin
        let prefix = origin.absoluteString + "/#pair="
        guard raw.hasPrefix(prefix), raw.utf8.count <= 512 else { throw MobileError.invalid("Use the complete Connect phone link from the approved Croptop publisher.") }
        let parts = raw.dropFirst(prefix.count).split(separator: ".", omittingEmptySubsequences: false)
        func canonicalSecret(_ value: Substring) -> Bool {
            guard value.utf8.count == 43,
                  value.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || $0 == 45 || $0 == 95 }),
                  let bytes = Data(base64Encoded: value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/") + "="), bytes.count == 32 else { return false }
            return bytes.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "") == value
        }
        guard parts.count == 2, parts.allSatisfy(canonicalSecret) else { throw MobileError.invalid("This connection link is incomplete or invalid.") }
        self.origin = origin; id = String(parts[0]); capability = String(parts[1])
    }
}

public enum ConnectionActionPolicy {
    public static func requireNewPairing(isBusy: Bool, hasConnection: Bool, hasPairing: Bool) throws {
        guard !isBusy else { throw MobileError.invalid("Finish the current operation before connecting a site. This link was not saved.") }
        guard !hasConnection else { throw MobileError.invalid("A site is already connected. Remove that connection before opening another Connect phone link.") }
        guard !hasPairing else { throw MobileError.invalid("Finish or cancel the current connection before opening another link.") }
    }

    public static func requireConnectionChange(isBusy: Bool, hasPairing: Bool) throws {
        guard !isBusy, !hasPairing else { throw MobileError.invalid("Finish or cancel the current operation before changing this connection.") }
    }
}
