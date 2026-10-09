import CryptoKit
import Foundation

public enum PairingCryptoError: Error, LocalizedError, Equatable {
    case invalidContext
    case invalidPublicKey
    case invalidEnvelope
    case authenticationFailed

    public var errorDescription: String? {
        switch self {
        case .invalidContext: return "The connection details are invalid. Create a new connection link."
        case .invalidPublicKey: return "The connection's device keys could not be verified."
        case .invalidEnvelope: return "The encrypted site key is incomplete. Create a new connection link."
        case .authenticationFailed: return "The encrypted site key could not be verified. Create a new connection link."
        }
    }
}

public struct PairingMaterial: Sendable {
    public let transcript: Data
    public let key: SymmetricKey
    /// Eight ungrouped decimal digits; formatting belongs to the view.
    public let code: String
}

/// The pure receiver-side pairing protocol. The caller additionally verifies
/// the claim's origin/ID against the opened link and checks current expiry.
/// Neither private key material nor the displayed code goes to the relay.
public enum PairingCrypto {
    public static func derive(privateKey: P256.KeyAgreement.PrivateKey, origin: String, id: String,
                              ipns: String, expiresAt: Int64, senderPublicKey: String,
                              receiverPublicKey: String) throws -> PairingMaterial {
        guard let url = URL(string: origin),
              (try? SessionChallengeValidator.validatedOrigin(url)) == origin,
              validID(id), expiresAt > 0,
              ipns.hasPrefix("k51"), ipns.utf8.count == 62,
              ipns.utf8.allSatisfy({ (97...122).contains($0) || (48...57).contains($0) }) else {
            throw PairingCryptoError.invalidContext
        }
        guard let sender = strictBase64(senderPublicKey, maximumBytes: 65), sender.count == 65, sender.first == 4,
              let receiver = strictBase64(receiverPublicKey, maximumBytes: 65), receiver.count == 65, receiver.first == 4,
              receiver == privateKey.publicKey.x963Representation,
              let peer = try? P256.KeyAgreement.PublicKey(x963Representation: sender) else {
            throw PairingCryptoError.invalidPublicKey
        }
        let lines = ["croptop-pairing-v1", origin, id, ipns, String(expiresAt), senderPublicKey, receiverPublicKey]
        let transcript = Data(lines.joined(separator: "\n").utf8)
        let secret = try privateKey.sharedSecretFromKeyAgreement(with: peer)
        let salt = Data(SHA256.hash(data: transcript))
        let key = secret.hkdfDerivedSymmetricKey(using: SHA256.self, salt: salt,
            sharedInfo: Data("croptop-pairing-key-v1".utf8), outputByteCount: 32)
        let codeBytes = secret.hkdfDerivedSymmetricKey(using: SHA256.self, salt: salt,
            sharedInfo: Data("croptop-pairing-code-v1".utf8), outputByteCount: 4)
        let codeNumber = codeBytes.withUnsafeBytes { $0.reduce(UInt32(0)) { ($0 << 8) | UInt32($1) } } % 100_000_000
        return PairingMaterial(transcript: transcript, key: key, code: String(format: "%08u", codeNumber))
    }

    public static func decrypt(nonce: String, ciphertext: String, material: PairingMaterial) throws -> Data {
        guard let nonceData = strictBase64(nonce, maximumBytes: 12), nonceData.count == 12,
              let encrypted = strictBase64(ciphertext, maximumBytes: 2048), encrypted.count > 16 else {
            throw PairingCryptoError.invalidEnvelope
        }
        do {
            let box = try AES.GCM.SealedBox(nonce: AES.GCM.Nonce(data: nonceData),
                ciphertext: encrypted.dropLast(16), tag: encrypted.suffix(16))
            return try AES.GCM.open(box, using: material.key, authenticating: material.transcript)
        } catch { throw PairingCryptoError.authenticationFailed }
    }

    private static func strictBase64(_ value: String, maximumBytes: Int) -> Data? {
        guard value.utf8.count <= ((maximumBytes + 2) / 3) * 4,
              let decoded = Data(base64Encoded: value), decoded.count <= maximumBytes,
              decoded.base64EncodedString() == value else { return nil }
        return decoded
    }

    private static func validID(_ value: String) -> Bool {
        guard value.utf8.count == 43,
              value.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0) ||
                  (48...57).contains($0) || $0 == 45 || $0 == 95 }) else { return false }
        let padded = value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/") + "="
        return strictBase64(padded, maximumBytes: 32)?.count == 32
    }
}
