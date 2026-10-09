import CryptoKit
import Foundation
import XCTest
@testable import CroptopMobileCore

private struct PairingFixture: Decodable {
    struct Info: Decodable {
        var id, origin, ipns, senderPublicKey, receiverPublicKey: String
        var expiresAt: Int64
    }
    let senderPrivateKey, receiverPrivateKey, transcript, sharedSecret, aesKey, confirmationCode: String
    let nonce, ciphertext, plaintext: String
    let info: Info

    static func load() throws -> Self {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let url = Bundle(for: PairingCryptoTests.self).url(forResource: "mobile-pairing-v1", withExtension: "json")
            ?? root.appendingPathComponent("testdata/mobile-pairing-v1.json")
        return try JSONDecoder().decode(Self.self, from: Data(contentsOf: url))
    }
}

final class PairingCryptoTests: XCTestCase {
    private func derive(_ info: PairingFixture.Info, _ key: P256.KeyAgreement.PrivateKey) throws -> PairingMaterial {
        try PairingCrypto.derive(privateKey: key, origin: info.origin, id: info.id, ipns: info.ipns,
            expiresAt: info.expiresAt, senderPublicKey: info.senderPublicKey, receiverPublicKey: info.receiverPublicKey)
    }

    private func receiver(_ fixture: PairingFixture) throws -> P256.KeyAgreement.PrivateKey {
        try P256.KeyAgreement.PrivateKey(rawRepresentation: XCTUnwrap(Data(base64Encoded: fixture.receiverPrivateKey)))
    }

    func testIndependentNodeFixtureMatchesTranscriptECDHCodeAndAES() throws {
        let fixture = try PairingFixture.load()
        let key = try receiver(fixture)
        let material = try derive(fixture.info, key)
        XCTAssertEqual(material.transcript, Data(fixture.transcript.utf8))
        XCTAssertEqual(material.code, fixture.confirmationCode)
        XCTAssertEqual(material.key.withUnsafeBytes { Data($0) }.base64EncodedString(), fixture.aesKey)
        let peer = try P256.KeyAgreement.PublicKey(x963Representation: XCTUnwrap(Data(base64Encoded: fixture.info.senderPublicKey)))
        let shared = try key.sharedSecretFromKeyAgreement(with: peer)
        XCTAssertEqual(shared.withUnsafeBytes { Data($0) }.base64EncodedString(), fixture.sharedSecret)
        let plaintext = try PairingCrypto.decrypt(nonce: fixture.nonce, ciphertext: fixture.ciphertext, material: material)
        XCTAssertEqual(plaintext, Data(fixture.plaintext.utf8))
        struct Transfer: Decodable { let pem, ipns: String }
        let transfer = try JSONDecoder().decode(Transfer.self, from: plaintext)
        XCTAssertEqual(try SiteIdentity(pem: transfer.pem).ipns, fixture.info.ipns)
        XCTAssertEqual(transfer.ipns, fixture.info.ipns)
    }

    func testRejectsMalformedKeysAndReceiverSubstitution() throws {
        let fixture = try PairingFixture.load(), key = try receiver(fixture)
        let badKeys = ["", fixture.info.senderPublicKey + "\n", String(fixture.info.senderPublicKey.dropLast()),
            Data(repeating: 0, count: 65).base64EncodedString(),
            (Data([4]) + Data(repeating: 0, count: 64)).base64EncodedString(),
            (Data([2]) + Data(repeating: 1, count: 32)).base64EncodedString()]
        for badKey in badKeys {
            var info = fixture.info; info.senderPublicKey = badKey
            XCTAssertThrowsError(try derive(info, key))
            info = fixture.info; info.receiverPublicKey = badKey
            XCTAssertThrowsError(try derive(info, key))
        }
        var info = fixture.info; info.receiverPublicKey = fixture.info.senderPublicKey
        XCTAssertThrowsError(try derive(info, key))
        XCTAssertThrowsError(try derive(fixture.info, P256.KeyAgreement.PrivateKey()))
    }

    func testRejectsAmbiguousOrInvalidTranscriptFields() throws {
        let fixture = try PairingFixture.load(), key = try receiver(fixture)
        let mutations: [(inout PairingFixture.Info) -> Void] = [
            { $0.origin += "/" }, { $0.origin = "http://app.crop.top" }, { $0.origin += "#fragment" },
            { $0.origin = "https://name:secret@app.crop.top" }, { $0.id += "\n" },
            { $0.id = String(repeating: "A", count: 42) + "B" }, // non-zero base64 padding bits
            { $0.id = String(repeating: "A", count: 42) }, { $0.ipns += "\n" },
            { $0.ipns = "k51" }, { $0.expiresAt = 0 }, { $0.expiresAt = -1 }
        ]
        for mutation in mutations {
            var info = fixture.info; mutation(&info)
            XCTAssertThrowsError(try derive(info, key))
        }
    }

    func testRejectsCiphertextNonceTagAndAADTampering() throws {
        let fixture = try PairingFixture.load(), key = try receiver(fixture)
        let material = try derive(fixture.info, key)
        let original = try XCTUnwrap(Data(base64Encoded: fixture.ciphertext))
        for index in [0, original.count / 2, original.count - 1] {
            var corrupted = original; corrupted[index] ^= 1
            XCTAssertThrowsError(try PairingCrypto.decrypt(nonce: fixture.nonce,
                ciphertext: corrupted.base64EncodedString(), material: material))
        }
        for nonce in ["", fixture.nonce + "\n", Data(repeating: 0, count: 11).base64EncodedString(),
                      Data(repeating: 0, count: 13).base64EncodedString(), Data(repeating: 0, count: 12).base64EncodedString()] {
            XCTAssertThrowsError(try PairingCrypto.decrypt(nonce: nonce, ciphertext: fixture.ciphertext, material: material))
        }
        for ciphertext in ["", fixture.ciphertext + "\n", Data(original.dropLast()).base64EncodedString(),
                           Data(original.prefix(16)).base64EncodedString(), Data(repeating: 0, count: 2049).base64EncodedString()] {
            XCTAssertThrowsError(try PairingCrypto.decrypt(nonce: fixture.nonce, ciphertext: ciphertext, material: material))
        }
        let alteredAAD = PairingMaterial(transcript: material.transcript + Data([10]), key: material.key, code: material.code)
        XCTAssertThrowsError(try PairingCrypto.decrypt(nonce: fixture.nonce, ciphertext: fixture.ciphertext, material: alteredAAD))
        var info = fixture.info; info.origin = "https://other.example"
        let wrongScope = try derive(info, key)
        XCTAssertThrowsError(try PairingCrypto.decrypt(nonce: fixture.nonce, ciphertext: fixture.ciphertext, material: wrongScope))
    }
}
