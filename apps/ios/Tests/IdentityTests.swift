import CryptoKit
import Foundation
import XCTest
@testable import CroptopMobileCore

struct MobileProtocolFixture: Decodable {
    struct Challenge: Decodable { let id, message, signature: String; let expiresAt: Int64 }
    let privateKeyPEM, publicKey, ipns, cid, sequence, host, origin: String
    let recordPayload, recordSignature, pushPayload, pushSignature: String
    let time, expiresAt: Int64
    let sessionChallenge: Challenge

    static func load() throws -> Self {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let url = Bundle(for: IdentityTests.self).url(forResource: "mobile-protocol-fixture", withExtension: "json")
            ?? root.appendingPathComponent("docs/design/mobile-protocol-fixture.json")
        return try JSONDecoder().decode(Self.self, from: Data(contentsOf: url))
    }
}

final class IdentityTests: XCTestCase {
    func testImportedIdentityMatchesGoIPNSAndSignatures() throws {
        let fixture = try MobileProtocolFixture.load()
        let identity = try SiteIdentity(pem: fixture.privateKeyPEM)
        XCTAssertEqual(identity.seed, Data((0..<32).map(UInt8.init)))
        XCTAssertEqual(identity.publicKey.base64EncodedString(), fixture.publicKey)
        XCTAssertEqual(identity.ipns, fixture.ipns)
        let verifier = try Curve25519.Signing.PublicKey(rawRepresentation: identity.publicKey)
        for (payload, signature) in [(fixture.recordPayload, fixture.recordSignature), (fixture.pushPayload, fixture.pushSignature)] {
            let bytes = try XCTUnwrap(Data(base64Encoded: payload))
            // CryptoKit can vary the nonce while remaining Ed25519 compatible;
            // verify both implementations' signatures, not byte equality.
            // https://developer.apple.com/documentation/cryptokit/curve25519/signing/privatekey/signature(for:)
            XCTAssertTrue(verifier.isValidSignature(try identity.sign(bytes), for: bytes))
            XCTAssertTrue(verifier.isValidSignature(try XCTUnwrap(Data(base64Encoded: signature)), for: bytes))
        }
        XCTAssertEqual(try SiteIdentity(seed: identity.seed).ipns, fixture.ipns)
    }

    func testRejectsUnsupportedOrAmbiguousPEM() throws {
        let fixture = try MobileProtocolFixture.load()
        let good = fixture.privateKeyPEM
        let body = good.components(separatedBy: "\n")[1]
        let der = try XCTUnwrap(Data(base64Encoded: body))
        var wrongAlgorithm = der
        wrongAlgorithm[11] = 0x6e
        let candidates = [
            good + good, "comment\n" + good, good + "trailing", good.replacingOccurrences(of: "PRIVATE KEY", with: "ENCRYPTED PRIVATE KEY"),
            good.replacingOccurrences(of: body, with: wrongAlgorithm.base64EncodedString()),
            good.replacingOccurrences(of: body, with: (der + Data([0])).base64EncodedString()),
            good.replacingOccurrences(of: body, with: Data(der.dropLast()).base64EncodedString()),
            good.replacingOccurrences(of: body, with: body + "!")
        ]
        for candidate in candidates { XCTAssertThrowsError(try SiteIdentity(pem: candidate)) }
        XCTAssertThrowsError(try SiteIdentity(seed: Data(repeating: 0, count: 31)))
        XCTAssertThrowsError(try SiteIdentity(seed: Data(repeating: 0, count: 64)))
        XCTAssertEqual(try SiteIdentity(pem: " \n" + good.replacingOccurrences(of: "\n", with: "\r\n") + " ").ipns, fixture.ipns)
    }

    func testSessionChallengeMatchesGoSignature() throws {
        let f = try MobileProtocolFixture.load(), challenge = f.sessionChallenge
        let payload = try SessionChallengeValidator.validate(id: challenge.id, message: challenge.message,
            expiresAt: challenge.expiresAt, origin: XCTUnwrap(URL(string: f.origin)), ipns: f.ipns,
            now: Date(timeIntervalSince1970: Double(f.time)))
        let identity = try SiteIdentity(pem: f.privateKeyPEM)
        let verifier = try Curve25519.Signing.PublicKey(rawRepresentation: identity.publicKey)
        XCTAssertTrue(verifier.isValidSignature(try identity.sign(payload), for: payload))
        XCTAssertTrue(verifier.isValidSignature(try XCTUnwrap(Data(base64Encoded: challenge.signature)), for: payload))
    }

    func testSessionRejectsChangedScopeAndExpiry() throws {
        let f = try MobileProtocolFixture.load(), challenge = f.sessionChallenge
        let now = Date(timeIntervalSince1970: Double(f.time))
        let origin = try XCTUnwrap(URL(string: f.origin))
        for message in [challenge.message + "\n", challenge.message.replacingOccurrences(of: f.origin, with: "https://evil.example"),
                        challenge.message.replacingOccurrences(of: f.ipns, with: "other-site")] {
            XCTAssertThrowsError(try SessionChallengeValidator.validate(id: challenge.id, message: message,
                expiresAt: challenge.expiresAt, origin: origin, ipns: f.ipns, now: now))
        }
        for expiry in [f.time, f.time - 1, f.time + 601, Int64.max] {
            let message = "croptop-mobile-session\n\(f.origin)\n\(f.ipns)\n\(challenge.id)\n\(expiry)"
            XCTAssertThrowsError(try SessionChallengeValidator.validate(id: challenge.id, message: message,
                expiresAt: expiry, origin: origin, ipns: f.ipns, now: now))
        }
        XCTAssertThrowsError(try SessionChallengeValidator.validate(id: challenge.id + "\n", message: challenge.message,
            expiresAt: challenge.expiresAt, origin: origin, ipns: f.ipns, now: now))
    }

    func testOriginRejectsInsecureRemoteAndURLAmbiguity() throws {
        for raw in ["http://crop.top", "https://name:secret@crop.top", "https://crop.top/path", "https://crop.top?other=1", "https://crop.top#fragment", "file:///crop.top"] {
            let url = try XCTUnwrap(URL(string: raw))
            XCTAssertThrowsError(try SessionChallengeValidator.validatedOrigin(url), raw)
        }
        for raw in ["https://crop.top", "https://crop.top:9443", "http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"] {
            XCTAssertEqual(try SessionChallengeValidator.validatedOrigin(XCTUnwrap(URL(string: raw + "/"))), raw)
        }
    }
}
