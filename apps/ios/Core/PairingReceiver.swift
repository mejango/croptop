import Foundation
import CryptoKit

public struct PairingEnvelope: Decodable, Sendable {
    public let id: String
    public let origin: String
    public let ipns: String
    public let senderPublicKey: String
    public let receiverPublicKey: String?
    public let expiresAt: Int64
    public let state: String
    public let nonce: String?
    public let ciphertext: String?
}

public struct ReceivedSite: Sendable {
    public let origin: URL
    public let name: String
    public let identity: SiteIdentity
}

/// Ephemeral receiver state deliberately remains in memory. A lost consume
/// response needs a new transfer; draft/operation recovery does not use it.
public actor PairingReceiver {
    public let origin: URL
    private let id: String
    private let capability: String
    private let key = P256.KeyAgreement.PrivateKey()
    private var envelope: PairingEnvelope?
    private var material: PairingMaterial?

    public init(link: String) throws {
        let parsed = try PairingLink(link)
        origin = parsed.origin; id = parsed.id; capability = parsed.capability
    }

    public init(link: PairingLink) {
        origin = link.origin; id = link.id; capability = link.capability
    }

    public func claim() async throws -> (ipns: String, code: String) {
        let response = try await request("claim", body: ["receiverPublicKey": key.publicKey.x963Representation.base64EncodedString()])
        try validate(response)
        let material = try PairingCrypto.derive(privateKey: key, origin: origin.absoluteString,
            id: id, ipns: response.ipns, expiresAt: response.expiresAt,
            senderPublicKey: response.senderPublicKey, receiverPublicKey: key.publicKey.x963Representation.base64EncodedString())
        self.material = material; envelope = response
        let code = material.code
        return (response.ipns, String(code.prefix(4)) + " " + String(code.suffix(4)))
    }

    public func consumeConfirmed() async throws -> ReceivedSite {
        guard let expected = envelope, let material else { throw MobileError.invalid("Start the connection again.") }
        let response = try await request("consume", body: ["confirmed": true])
        try validate(response)
        guard response.ipns == expected.ipns, response.senderPublicKey == expected.senderPublicKey,
              response.expiresAt == expected.expiresAt, response.state == "consumed",
              let nonce = response.nonce, let ciphertext = response.ciphertext else {
            throw MobileError.invalid("The connection response changed. Create a new connection link.")
        }
        let plaintext = try PairingCrypto.decrypt(nonce: nonce, ciphertext: ciphertext, material: material)
        struct Transfer: Decodable { let version: Int; let origin: String; let ipns: String; let name: String; let pem: String }
        let transfer = try JSONDecoder().decode(Transfer.self, from: plaintext)
        let identity = try SiteIdentity(pem: transfer.pem)
        guard transfer.version == 1, transfer.origin == origin.absoluteString,
              transfer.ipns == expected.ipns, identity.ipns == expected.ipns else {
            throw MobileError.invalid("The received key does not belong to the confirmed site.")
        }
        return ReceivedSite(origin: origin, name: transfer.name, identity: identity)
    }

    private func validate(_ response: PairingEnvelope) throws {
        let now = Int64(Date().timeIntervalSince1970)
        guard response.id == id, response.origin == origin.absoluteString,
              response.receiverPublicKey == key.publicKey.x963Representation.base64EncodedString(),
              response.expiresAt > now, response.expiresAt <= now + 660,
              response.ipns.hasPrefix("k51"), response.ipns.count < 100 else {
            throw MobileError.invalid("The connection is expired or belongs to another destination.")
        }
    }

    private func request(_ action: String, body: [String: Any]) async throws -> PairingEnvelope {
        var request = URLRequest(url: origin.appendingPathComponent("v0/mobile/pairings/\(id)/\(action)"))
        request.httpMethod = "POST"; request.timeoutInterval = 30
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue(capability, forHTTPHeaderField: "X-Croptop-Pairing")
        let config = URLSessionConfiguration.ephemeral; config.httpCookieStorage = nil; config.urlCache = nil
        let session = URLSession(configuration: config, delegate: NoRedirects(), delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let (data, rawResponse) = try await session.data(for: request)
        guard let response = rawResponse as? HTTPURLResponse, (200..<300).contains(response.statusCode) else {
            struct Failure: Decodable { let error: String; let code: String }
            let error = try? JSONDecoder().decode(Failure.self, from: data)
            throw MobileError.service((rawResponse as? HTTPURLResponse)?.statusCode ?? 0, error?.code ?? "pairing_error", error?.error ?? "The connection could not finish. Confirm on your publisher, then try again.")
        }
        return try JSONDecoder().decode(PairingEnvelope.self, from: data)
    }
}
