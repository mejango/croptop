import Foundation
import Security
import ImageIO
import UniformTypeIdentifiers
import UIKit
import CroptopMobileCore

enum DeviceStorage {
    static let appGroup = "group.top.crop.mobile"
    // Offline capture memory bound; service configuration owns publish limits.
    static let maxCaptureImageBytes = 64 * 1024 * 1024

    static func drafts() throws -> DraftStore {
        guard let root = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup) else {
            throw MobileError.storage("Shared draft storage is unavailable. This build needs the Croptop App Group entitlement.")
        }
        return try DraftStore(root: root.appendingPathComponent("Drafts", isDirectory: true))
    }

    private static func keyQuery(ipns: String) throws -> [String: Any] {
        guard let group = Bundle.main.object(forInfoDictionaryKey: "CroptopKeychainGroup") as? String,
              !group.isEmpty, !group.contains("$(") else {
            throw MobileError.storage("The shared publishing Keychain is not configured in this build.")
        }
        return [kSecClass as String: kSecClassGenericPassword,
                kSecAttrService as String: "top.crop.mobile.site-key",
                kSecAttrAccount as String: ipns,
                kSecAttrAccessGroup as String: group,
                kSecAttrSynchronizable as String: false]
    }

    static func saveIdentity(_ identity: SiteIdentity) throws {
        let query = try keyQuery(ipns: identity.ipns)
        let value: [String: Any] = [kSecValueData as String: identity.seed,
                                  kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        let update = SecItemUpdate(query as CFDictionary, value as CFDictionary)
        let result: OSStatus
        if update == errSecItemNotFound { result = SecItemAdd(query.merging(value) { _, new in new } as CFDictionary, nil) }
        else { result = update }
        guard result == errSecSuccess else { throw MobileError.storage("The site key could not be saved securely (\(result)). Your site is not connected.") }
    }

    static func identity(ipns: String) throws -> SiteIdentity {
        var query = try keyQuery(ipns: ipns)
        query[kSecReturnData as String] = true; query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess, let data = item as? Data else {
            throw MobileError.storage("Unlock your phone or reconnect this site's key to resume publishing.")
        }
        let identity = try SiteIdentity(seed: data)
        guard identity.ipns == ipns else { throw MobileError.storage("The saved key belongs to a different site.") }
        return identity
    }

    static func removeIdentity(ipns: String) throws {
        let status = SecItemDelete(try keyQuery(ipns: ipns) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw MobileError.storage("The device key could not be removed (\(status)).") }
    }

    static func inspectImage(_ data: Data) throws -> String {
        guard !data.isEmpty, data.count <= maxCaptureImageBytes,
              let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary),
              CGImageSourceGetCount(source) == 1,
              let type = CGImageSourceGetType(source) as String? else {
            throw MobileError.invalid("Choose one still image. Animated, empty or oversized files cannot be saved.")
        }
        let formats = ["public.png": "image/png", "public.jpeg": "image/jpeg", "org.webmproject.webp": "image/webp",
                       "public.heic": "image/heic", "public.heif": "image/heif"]
        guard let contentType = formats[type] else { throw MobileError.invalid("Choose a PNG, JPEG, WebP or HEIF still image.") }
        return contentType
    }

    static func thumbnail(url: URL) -> UIImage? {
        guard let source = CGImageSourceCreateWithURL(url as CFURL, [kCGImageSourceShouldCache: false] as CFDictionary),
              let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [
                kCGImageSourceCreateThumbnailFromImageAlways: true,
                kCGImageSourceCreateThumbnailWithTransform: true,
                kCGImageSourceThumbnailMaxPixelSize: 1280
              ] as CFDictionary) else { return nil }
        return UIImage(cgImage: image)
    }

    static func validatePixels(_ data: Data, maxPixels: Int) throws {
        guard let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary),
              let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let width = properties[kCGImagePropertyPixelWidth] as? Int,
              let height = properties[kCGImagePropertyPixelHeight] as? Int,
              width > 0, height > 0, width <= maxPixels, height <= maxPixels / width else {
            throw MobileError.invalid("Choose a smaller image within this service's pixel limit.")
        }
    }
}
