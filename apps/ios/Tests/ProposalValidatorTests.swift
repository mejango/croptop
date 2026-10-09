import CryptoKit
import Foundation
import XCTest
@testable import CroptopMobileCore

final class ProposalValidatorTests: XCTestCase {
    private func context(_ f: MobileProtocolFixture) -> ProposalValidationContext {
        let image = Data("the privately fetched normalized image".utf8)
        let id = "0185B829-D980-469D-9681-DB87486884BE"
        return ProposalValidationContext(expectedID: id, expectedIPNS: f.ipns, expectedTitle: "Title",
            expectedCaption: "A saved caption", expectedHost: f.host, operationID: id, operationIPNS: f.ipns,
            postID: id, title: "Title", caption: "A saved caption",
            mediaSHA256: SHA256.hash(data: image).map { String(format: "%02x", $0) }.joined(),
            mediaType: "image/png", proposalID: "fixture-proposal", cid: f.cid, parent: f.cid,
            sequence: f.sequence, host: f.host, time: f.time, expiresAt: f.expiresAt,
            recordPayload: f.recordPayload, pushPayload: f.pushPayload, normalizedImage: image)
    }

    func testValidatesGoFixtureBeforeSigning() throws {
        let f = try MobileProtocolFixture.load()
        let proposal = try ProposalValidator.validate(context(f), now: Date(timeIntervalSince1970: Double(f.time)))
        let key = try SiteIdentity(pem: f.privateKeyPEM)
        let verifier = try Curve25519.Signing.PublicKey(rawRepresentation: key.publicKey)
        for (payload, fixtureSignature) in [(proposal.recordPayload, f.recordSignature), (proposal.pushPayload, f.pushSignature)] {
            XCTAssertTrue(verifier.isValidSignature(try key.sign(payload), for: payload))
            XCTAssertTrue(verifier.isValidSignature(try XCTUnwrap(Data(base64Encoded: fixtureSignature)), for: payload))
        }
    }

    func testRejectsChangedDraftScopeImageAndTiming() throws {
        let f = try MobileProtocolFixture.load()
        let mutations: [(String, (inout ProposalValidationContext) -> Void)] = [
            ("operation", { $0.operationID = UUID().uuidString }),
            ("post", { $0.postID = UUID().uuidString }),
            ("destination", { $0.operationIPNS = "other-site" }),
            ("title", { $0.title += " changed" }),
            ("caption", { $0.caption += " changed" }),
            ("host", { $0.host = "evil.example" }),
            ("image", { $0.normalizedImage.append(0) }),
            ("hash", { $0.mediaSHA256 = String(repeating: "0", count: 64) }),
            ("type", { $0.mediaType = "text/html" }),
            ("empty image", { $0.normalizedImage = Data() }),
            ("expired", { $0.expiresAt = f.time }),
            ("future", { $0.time = f.time + 601 }),
            ("unbounded expiry", { $0.expiresAt = Int64.max }),
            ("leading zero", { $0.sequence = "042" }),
            ("negative", { $0.sequence = "-1" }),
            ("overflow", { $0.sequence = "9223372036854775808" }),
            ("CID path", { $0.cid += "/index.html" }),
            ("invalid parent", { $0.parent = "babc" }),
            ("proposal", { $0.proposalID = "" }),
            ("arbitrary push bytes", { $0.pushPayload = Data("sign me".utf8).base64EncodedString() }),
            ("noncanonical base64", { $0.recordPayload += "\n" })
        ]
        for (name, mutate) in mutations {
            var c = context(f); mutate(&c)
            XCTAssertThrowsError(try ProposalValidator.validate(c, now: Date(timeIntervalSince1970: Double(f.time))), name)
        }
    }

    func testRejectsNonCanonicalCBORAndRecordSubstitution() throws {
        let f = try MobileProtocolFixture.load()
        let original = try XCTUnwrap(Data(base64Encoded: f.recordPayload))
        let prefixCount = "ipns-signature:".utf8.count
        func replacing(_ source: Data, _ target: Data) throws -> Data {
            var result = original
            result.replaceSubrange(try XCTUnwrap(result.range(of: source)), with: target)
            return result
        }
        var wrongPrefix = original; wrongPrefix[0] = 0
        var extraFields = original; extraFields[prefixCount] = 0xa6
        var noncanonicalMap = original; noncanonicalMap.replaceSubrange(prefixCount...prefixCount, with: [0xb8, 0x05])
        var negativeSequence = original
        let sequenceOffset = try XCTUnwrap(negativeSequence.range(of: Data("Sequence".utf8))).upperBound
        negativeSequence[sequenceOffset] = 0x38
        let invalidRecords = [
            wrongPrefix, extraFields, noncanonicalMap, negativeSequence, original + Data([0]), Data(original.dropLast()),
            try replacing(Data("TTL".utf8), Data("XXX".utf8)),
            try replacing(Data("/ipfs/".utf8), Data("/ipns/".utf8)),
            try replacing(Data("Sequence".utf8) + Data([0x18, 0x2a]), Data("Sequence".utf8) + Data([0x19, 0, 0x2a])),
            try replacing(Data("Sequence".utf8) + Data([0x18, 0x2a]), Data("Sequence".utf8) + Data([0x18, 0x2b])),
            try replacing(Data("2099-10-28T00:00:00Z".utf8), Data("2099-01-01T00:00:00Z".utf8)),
            try replacing(Data("2099-10-28T00:00:00Z".utf8), Data("2199-10-28T00:00:00Z".utf8))
        ]
        for (index, record) in invalidRecords.enumerated() {
            var c = context(f); c.recordPayload = record.base64EncodedString()
            XCTAssertThrowsError(try ProposalValidator.validate(c, now: Date(timeIntervalSince1970: Double(f.time))), "mutation \(index)")
        }
        // Every possible truncation must fail safely, without out-of-bounds reads.
        for length in 0..<original.count {
            var c = context(f); c.recordPayload = original.prefix(length).base64EncodedString()
            XCTAssertThrowsError(try ProposalValidator.validate(c, now: Date(timeIntervalSince1970: Double(f.time))))
        }
    }

    func testAcceptsRFC3339NanosecondValidityFromGo() throws {
        let f = try MobileProtocolFixture.load()
        var payload = try XCTUnwrap(Data(base64Encoded: f.recordPayload))
        let seconds = Data([0x54]) + Data("2099-10-28T00:00:00Z".utf8)
        let nanoseconds = Data([0x58, 0x1e]) + Data("2099-10-28T00:00:00.123456789Z".utf8)
        // The byte-string length is 30 for a timestamp with nine fractional digits.
        XCTAssertEqual(nanoseconds.count - 2, 30)
        payload.replaceSubrange(try XCTUnwrap(payload.range(of: seconds)), with: nanoseconds)
        var c = context(f); c.recordPayload = payload.base64EncodedString()
        XCTAssertNoThrow(try ProposalValidator.validate(c, now: Date(timeIntervalSince1970: Double(f.time))))
    }
}
