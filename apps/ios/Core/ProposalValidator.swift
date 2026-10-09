import CryptoKit
import Foundation

public struct ProposalValidationContext {
    public var expectedID, expectedIPNS, expectedTitle, expectedCaption, expectedHost: String
    public var operationID, operationIPNS, postID, title, caption: String
    public var mediaSHA256, mediaType, proposalID, cid, parent, sequence, host: String
    public var time, expiresAt: Int64
    public var recordPayload, pushPayload: String
    public var normalizedImage: Data

    public init(expectedID: String, expectedIPNS: String, expectedTitle: String, expectedCaption: String,
                expectedHost: String, operationID: String, operationIPNS: String, postID: String,
                title: String, caption: String, mediaSHA256: String, mediaType: String,
                proposalID: String, cid: String, parent: String, sequence: String, host: String,
                time: Int64, expiresAt: Int64, recordPayload: String, pushPayload: String,
                normalizedImage: Data) {
        self.expectedID = expectedID; self.expectedIPNS = expectedIPNS
        self.expectedTitle = expectedTitle; self.expectedCaption = expectedCaption; self.expectedHost = expectedHost
        self.operationID = operationID; self.operationIPNS = operationIPNS; self.postID = postID
        self.title = title; self.caption = caption; self.mediaSHA256 = mediaSHA256; self.mediaType = mediaType
        self.proposalID = proposalID; self.cid = cid; self.parent = parent; self.sequence = sequence
        self.host = host; self.time = time; self.expiresAt = expiresAt
        self.recordPayload = recordPayload; self.pushPayload = pushPayload; self.normalizedImage = normalizedImage
    }
}

public struct ValidatedProposal {
    public let recordPayload: Data
    public let pushPayload: Data
}

public enum ProposalValidator {
    public static func validate(_ context: ProposalValidationContext, now: Date = Date()) throws -> ValidatedProposal {
        let c = context
        guard UUID(uuidString: c.expectedID)?.uuidString == c.expectedID,
              c.operationID == c.expectedID, c.postID == c.expectedID,
              c.operationIPNS == c.expectedIPNS, c.title == c.expectedTitle, c.caption == c.expectedCaption else {
            throw invalid("the destination or draft changed")
        }
        guard !c.proposalID.isEmpty, c.proposalID.utf8.count <= 256,
              !c.expectedHost.isEmpty, c.host == c.expectedHost,
              c.host.unicodeScalars.allSatisfy({ !$0.properties.isWhitespace && $0.value > 32 && $0.value < 127 }),
              validCID(c.cid), validCID(c.parent) else { throw invalid("the publishing destination is invalid") }
        guard !c.sequence.isEmpty, c.sequence.utf8.allSatisfy({ (48...57).contains($0) }),
              let sequence = UInt64(c.sequence), sequence <= UInt64(Int64.max), String(sequence) == c.sequence else {
            throw invalid("the publication sequence is invalid")
        }
        let timestamp = now.timeIntervalSince1970
        guard abs(Double(c.time) - timestamp) < 600, Double(c.expiresAt) > timestamp,
              c.expiresAt > c.time, Double(c.expiresAt) <= Double(c.time) + 600 else {
            throw invalid("the publication request expired")
        }
        guard ["image/png", "image/jpeg", "image/webp"].contains(c.mediaType), !c.normalizedImage.isEmpty,
              hex(SHA256.hash(data: c.normalizedImage)) == c.mediaSHA256 else {
            throw invalid("the image does not match its preview")
        }
        let expectedPush = Data("croptop-push\n\(c.host)\n\(c.expectedIPNS)\n\(c.cid)\n\(c.sequence)\n\(c.time)".utf8)
        guard let push = strictBase64(c.pushPayload), push == expectedPush,
              let record = strictBase64(c.recordPayload), record.count <= 1024 else {
            throw invalid("the signing request does not match this post")
        }
        try validateRecord(record, cid: c.cid, sequence: sequence, time: c.time, expiresAt: c.expiresAt)
        return ValidatedProposal(recordPayload: record, pushPayload: expectedPush)
    }

    private static func validateRecord(_ payload: Data, cid: String, sequence: UInt64, time: Int64, expiresAt: Int64) throws {
        let prefix = Data("ipns-signature:".utf8)
        guard payload.starts(with: prefix) else { throw invalid("the IPNS signature prefix is invalid") }
        var reader = CBORReader(bytes: Array(payload.dropFirst(prefix.count)))
        guard try reader.token(major: 5) == 5 else { throw invalid("the IPNS record fields changed") }
        try reader.key("TTL")
        guard try reader.token(major: 0) == 60_000_000_000 else { throw invalid("the IPNS cache lifetime changed") }
        try reader.key("Value")
        guard try reader.bytes(major: 2) == Array("/ipfs/\(cid)".utf8) else { throw invalid("the IPNS content identifier changed") }
        try reader.key("Sequence")
        guard try reader.token(major: 0) == sequence else { throw invalid("the IPNS sequence changed") }
        try reader.key("Validity")
        let validityBytes = try reader.bytes(major: 2)
        guard let validity = String(bytes: validityBytes, encoding: .utf8),
              validity.range(of: #"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$"#, options: .regularExpression) != nil,
              let date = parseValidity(validity), date.timeIntervalSince1970 > Double(expiresAt),
              date.timeIntervalSince1970 <= Double(time) + 7200 * 3600 + 60 else {
            throw invalid("the IPNS record expiry is invalid")
        }
        try reader.key("ValidityType")
        guard try reader.token(major: 0) == 0, reader.finished else { throw invalid("the IPNS record has unexpected data") }
    }

    private static func parseValidity(_ value: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = value.contains(".") ? [.withInternetDateTime, .withFractionalSeconds] : [.withInternetDateTime]
        return formatter.date(from: value)
    }

    private static func strictBase64(_ value: String) -> Data? {
        guard value.utf8.count <= 2048, let data = Data(base64Encoded: value), data.base64EncodedString() == value else { return nil }
        return data
    }

    private static func hex(_ digest: SHA256.Digest) -> String { digest.map { String(format: "%02x", $0) }.joined() }
    private static func invalid(_ reason: String) -> SigningValidationError { .invalidProposal(reason) }

    /// A CID must be a single canonical root identifier, never a URL or path.
    /// The publishing service emits CIDv1 in lower base32; CIDv0 remains valid
    /// for existing published parents. Decode enough to verify the envelope.
    private static func validCID(_ value: String) -> Bool {
        if value.hasPrefix("Qm"), value.utf8.count == 46 {
            let alphabet = Array("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz".utf8)
            var decoded = [UInt8](repeating: 0, count: 34)
            for char in value.utf8 {
                guard let digit = alphabet.firstIndex(of: char) else { return false }
                var carry = digit
                for i in decoded.indices.reversed() { carry += Int(decoded[i]) * 58; decoded[i] = UInt8(carry & 255); carry >>= 8 }
                if carry != 0 { return false }
            }
            return decoded.starts(with: [0x12, 0x20])
        }
        guard value.first == "b", (5...128).contains(value.utf8.count) else { return false }
        let alphabet = Array("abcdefghijklmnopqrstuvwxyz234567".utf8)
        var decoded = [UInt8](), bits = 0, accumulator = 0
        for char in value.utf8.dropFirst() {
            guard let digit = alphabet.firstIndex(of: char) else { return false }
            accumulator = (accumulator << 5) | digit; bits += 5
            if bits >= 8 { bits -= 8; decoded.append(UInt8((accumulator >> bits) & 255)); accumulator &= (1 << bits) - 1 }
        }
        guard accumulator == 0, bits < 5 else { return false }
        var offset = 0
        func varint() -> UInt64? {
            var result: UInt64 = 0
            for i in 0..<9 {
                guard offset < decoded.count else { return nil }
                let byte = decoded[offset]; offset += 1
                result |= UInt64(byte & 0x7f) << (i * 7)
                if byte & 0x80 == 0 { return i > 0 && byte == 0 ? nil : result }
            }
            return nil
        }
        guard varint() == 1, let codec = varint(), codec > 0,
              let hash = varint(), let length = varint(), length <= 64,
              length == UInt64(decoded.count - offset) else { return false }
        return hash == 0 || length > 0
    }

    /// A deliberately small canonical DAG-CBOR reader for the five IPNS fields.
    /// No recursion, indefinite lengths, tags, floats or arbitrary CBOR values.
    private struct CBORReader {
        var bytes: [UInt8]
        var offset = 0
        var finished: Bool { offset == bytes.count }

        mutating func token(major: UInt8) throws -> UInt64 {
            guard offset < bytes.count else { throw invalid("the IPNS record is truncated") }
            let first = bytes[offset]; offset += 1
            guard first >> 5 == major else { throw invalid("the IPNS record field type changed") }
            let small = first & 31
            if small < 24 { return UInt64(small) }
            guard (24...27).contains(small) else { throw invalid("the IPNS encoding is unsupported") }
            let count = 1 << Int(small - 24)
            guard bytes.count - offset >= count else { throw invalid("the IPNS record is truncated") }
            var result: UInt64 = 0
            for _ in 0..<count { result = (result << 8) | UInt64(bytes[offset]); offset += 1 }
            let minimum: UInt64 = count == 1 ? 24 : 1 << ((count / 2) * 8)
            guard result >= minimum, result <= UInt64(Int64.max) else { throw invalid("the IPNS encoding is not canonical") }
            return result
        }

        mutating func bytes(major: UInt8) throws -> [UInt8] {
            let length = try token(major: major)
            guard length <= UInt64(bytes.count - offset) else { throw invalid("the IPNS record is truncated") }
            let end = offset + Int(length)
            defer { offset = end }
            return Array(bytes[offset..<end])
        }

        mutating func key(_ expected: String) throws {
            guard try bytes(major: 3) == Array(expected.utf8) else { throw invalid("the IPNS record fields changed") }
        }
    }
}
