import Foundation
import CoreTransferable
import UniformTypeIdentifiers
import CroptopMobileCore

enum ImageIntake {
    static func persist(providers: [NSItemProvider], store: DraftStore, destination: SiteConnection?) async throws -> LocalDraft {
        guard providers.count == 1,
              let provider = providers.first,
              provider.hasItemConformingToTypeIdentifier(UTType.image.identifier),
              let identifier = provider.registeredTypeIdentifiers.first(where: { UTType($0)?.conforms(to: .image) == true }) else {
            throw MobileError.invalid("Share exactly one still image with Croptop. No images were discarded or published.")
        }
        return try await withCheckedThrowingContinuation { continuation in
            provider.loadFileRepresentation(forTypeIdentifier: identifier) { temporaryURL, error in
                do {
                    if let error { throw error }
                    guard let temporaryURL else {
                        throw MobileError.invalid("The image could not be downloaded. Open it in Photos and share again.")
                    }
                    // The provider may delete its file when this callback ends.
                    // Validate and persist the entire draft before returning.
                    let data = try BoundedFile.read(url: temporaryURL, maxBytes: DeviceStorage.maxCaptureImageBytes)
                    let contentType = try DeviceStorage.inspectImage(data)
                    let draft = try store.create(image: data, contentType: contentType, destination: destination)
                    continuation.resume(returning: draft)
                } catch { continuation.resume(throwing: error) }
            }
        }
    }
}

/// PhotosPicker's file is also temporary. Capture bounded bytes within its
/// import callback instead of asking PhotosPicker for an unbounded Data value.
struct ScreenshotTransfer: Transferable, Sendable {
    let data: Data

    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .image) { received in
            let data = try BoundedFile.read(url: received.file, maxBytes: DeviceStorage.maxCaptureImageBytes)
            _ = try DeviceStorage.inspectImage(data)
            return ScreenshotTransfer(data: data)
        }
    }
}
