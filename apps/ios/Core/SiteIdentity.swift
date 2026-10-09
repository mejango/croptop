import CryptoKit
import Foundation

public enum SigningValidationError: Error, LocalizedError, Equatable {
    case invalidKey
    case invalidOrigin
    case invalidChallenge
    case invalidProposal(String)

    public var errorDescription: String? {
        switch self {
        case .invalidKey: return "Choose an unencrypted Ed25519 Croptop site key in PEM format."
        case .invalidOrigin: return "Connect using a secure Croptop service address."
        case .invalidChallenge: return "The connection request could not be verified. Try connecting again."
        case .invalidProposal(let reason): return "The publication could not be verified: \(reason). Your draft is still saved."
        }
    }
}

/// RFC 8410 Ed25519 keys only. Imported seed material belongs in the platform
/// keychain; neither this seed nor the original PEM belongs in API requests.
public struct SiteIdentity: Sendable {
    private let key: Curve25519.Signing.PrivateKey

    public init(seed: Data) throws {
        guard seed.count == 32 else { throw SigningValidationError.invalidKey }
        do { key = try Curve25519.Signing.PrivateKey(rawRepresentation: seed) }
        catch { throw SigningValidationError.invalidKey }
    }

    public init(pem: String) throws {
        let trimmed = pem.trimmingCharacters(in: .whitespacesAndNewlines)
        let begin = "-----BEGIN PRIVATE KEY-----"
        let end = "-----END PRIVATE KEY-----"
        guard trimmed.utf8.count < 1024, trimmed.hasPrefix(begin), trimmed.hasSuffix(end) else {
            throw SigningValidationError.invalidKey
        }
        let body = trimmed.dropFirst(begin.count).dropLast(end.count)
        let base64 = String(String.UnicodeScalarView(body.unicodeScalars.filter { ![9, 10, 13, 32].contains($0.value) }))
        // Reject attributes, encrypted keys, alternate algorithms, extra DER
        // fields, and trailing bytes rather than guessing where the seed lives.
        let prefix = Data([0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06,
                           0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20])
        guard let der = Data(base64Encoded: base64), der.base64EncodedString() == base64,
              der.count == prefix.count + 32, der.starts(with: prefix) else {
            throw SigningValidationError.invalidKey
        }
        try self.init(seed: Data(der.dropFirst(prefix.count)))
    }

    public var seed: Data { key.rawRepresentation }
    public var publicKey: Data { key.publicKey.rawRepresentation }

    public var ipns: String {
        // CIDv1/libp2p-key + identity multihash of the Ed25519 public-key
        // protobuf (field 1 type=1, field 2 32 raw bytes), in lower base36.
        let cid = [UInt8]([0x01, 0x72, 0x00, 0x24, 0x08, 0x01, 0x12, 0x20]) + publicKey
        return "k" + Self.base36(Array(cid))
    }

    public func sign(_ payload: Data) throws -> Data { try key.signature(for: payload) }

    private static func base36(_ bytes: [UInt8]) -> String {
        let alphabet = Array("0123456789abcdefghijklmnopqrstuvwxyz".utf8)
        var remainderBytes = bytes
        var digits = [UInt8]()
        while !remainderBytes.isEmpty {
            var quotient = [UInt8]()
            var carry = 0
            for byte in remainderBytes {
                let value = carry * 256 + Int(byte)
                let next = value / 36
                carry = value % 36
                if next != 0 || !quotient.isEmpty { quotient.append(UInt8(next)) }
            }
            digits.append(alphabet[carry])
            remainderBytes = quotient
        }
        return String(decoding: digits.reversed(), as: UTF8.self)
    }
}

public enum SessionChallengeValidator {
    /// Return only the exact, locally reconstructed challenge bytes to sign.
    public static func validate(id: String, message: String, expiresAt: Int64,
                                origin: URL, ipns: String, now: Date = Date()) throws -> Data {
        let originText = try validatedOrigin(origin)
        let allowedID = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_")
        guard (16...128).contains(id.utf8.count), id.unicodeScalars.allSatisfy(allowedID.contains),
              ipns.hasPrefix("k51"), ipns.utf8.count == 62,
              ipns.utf8.allSatisfy({ (97...122).contains($0) || (48...57).contains($0) }),
              Double(expiresAt) > now.timeIntervalSince1970,
              Double(expiresAt) <= now.timeIntervalSince1970 + 600 else {
            throw SigningValidationError.invalidChallenge
        }
        let expected = "croptop-mobile-session\n\(originText)\n\(ipns)\n\(id)\n\(expiresAt)"
        guard message == expected else { throw SigningValidationError.invalidChallenge }
        return Data(expected.utf8)
    }

    public static func validatedOrigin(_ origin: URL) throws -> String {
        guard let parts = URLComponents(url: origin, resolvingAgainstBaseURL: false),
              let host = parts.host, !host.isEmpty,
              parts.user == nil, parts.password == nil, parts.query == nil, parts.fragment == nil,
              parts.path.isEmpty || parts.path == "/",
              parts.scheme == "https" || (parts.scheme == "http" &&
                  ["localhost", "127.0.0.1", "::1", "[::1]"].contains(host)) else {
            throw SigningValidationError.invalidOrigin
        }
        var canonical = parts
        canonical.path = ""
        guard let result = canonical.string else { throw SigningValidationError.invalidOrigin }
        return result
    }
}
