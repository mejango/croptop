import XCTest
import UIKit
import ImageIO
import UniformTypeIdentifiers
import CroptopMobileCore

final class ImageIntakeTests: XCTestCase {
    private var directory: URL!
    private var store: DraftStore!

    override func setUpWithError() throws {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("croptop-intake-test-\(UUID().uuidString)", isDirectory: true)
        store = try DraftStore(root: directory.appendingPathComponent("drafts", isDirectory: true))
    }

    override func tearDownWithError() throws { try FileManager.default.removeItem(at: directory) }

    func testRealProviderPersistsBeforeItsTemporaryFileExpires() async throws {
        let bytes = png()
        let provider = ObservedItemProvider()
        provider.registerDataRepresentation(forTypeIdentifier: UTType.png.identifier, visibility: .all) { completion in
            completion(bytes, nil)
            return nil
        }
        let callbackEnded = expectation(description: "Provider callback ended and its temporary file expired")
        let draftRoot = store.root
        provider.afterConsumer = { temporaryURL in
            defer { callbackEnded.fulfill() }
            do {
                // This observer runs synchronously just after the production
                // callback. An asynchronously deferred save fails this check.
                let reopened = try DraftStore(root: draftRoot)
                let saved = try XCTUnwrap(reopened.list().first)
                XCTAssertEqual(try Data(contentsOf: reopened.imageURL(saved)), bytes)
                let temporaryURL = try XCTUnwrap(temporaryURL)
                XCTAssertNotEqual(temporaryURL, reopened.imageURL(saved))
                // Enforce the provider's documented lifetime immediately.
                try FileManager.default.removeItem(at: temporaryURL)
                XCTAssertFalse(FileManager.default.fileExists(atPath: temporaryURL.path))
            } catch { XCTFail("Image must be durable before provider callback returns: \(error)") }
        }

        let draft = try await ImageIntake.persist(providers: [provider], store: store, destination: nil)
        await fulfillment(of: [callbackEnded], timeout: 5)
        let reopened = try DraftStore(root: draftRoot)
        XCTAssertEqual(try reopened.load(draft.id), draft)
        XCTAssertEqual(try Data(contentsOf: reopened.imageURL(draft)), bytes)
        XCTAssertEqual(draft.contentType, "image/png")
    }

    func testShareBeforeSetupRetainsImageThroughConnectionAndCaptionReopen() async throws {
        let bytes = png()
        let draft = try await ImageIntake.persist(providers: [imageProvider(bytes)], store: store, destination: nil)
        XCTAssertNil(draft.destination)
        XCTAssertFalse(draft.submitted)
        XCTAssertThrowsError(try store.beginSubmission(draft.id))

        let connection = SiteConnection(origin: URL(string: "https://app.crop.top")!, ipns: "k51-test-site", name: "Test site")
        try store.setConnection(connection)
        _ = try store.edit(draft.id, title: "Screenshot", caption: "Saved before setup 🌱", destination: connection)
        let reopened = try DraftStore(root: store.root)
        let restored = try reopened.load(draft.id)
        XCTAssertEqual(restored.title, "Screenshot")
        XCTAssertEqual(restored.caption, "Saved before setup 🌱")
        XCTAssertEqual(restored.destination, connection)
        XCTAssertEqual(try Data(contentsOf: reopened.imageURL(restored)), bytes)
        XCTAssertEqual(try reopened.list().count, 1)
    }

    func testConnectedCaptureUsesTheProvidedDestination() async throws {
        let connection = SiteConnection(origin: URL(string: "https://app.crop.top")!, ipns: "k51-test-site", name: "Test site")
        let draft = try await ImageIntake.persist(providers: [imageProvider(png())], store: store, destination: connection)
        XCTAssertEqual(try store.load(draft.id).destination, connection)
    }

    func testRejectsZeroOrMultipleAttachmentsWithoutSavingAny() async throws {
        try await expectRejected([])
        try await expectRejected([imageProvider(png()), imageProvider(png())])
        let text = NSItemProvider(object: "Extra attachment" as NSString)
        try await expectRejected([imageProvider(png()), text])
    }

    func testRejectsUnsupportedAttachmentAndFalseImageTypeWithoutSavingAny() async throws {
        try await expectRejected([NSItemProvider(object: "Not an image" as NSString)])
        try await expectRejected([imageProvider(Data("Not a PNG".utf8))])
        try await expectRejected([imageProvider(Data())])
    }

    func testProviderFailureDoesNotCreateADraft() async throws {
        let provider = NSItemProvider()
        provider.registerDataRepresentation(forTypeIdentifier: UTType.png.identifier, visibility: .all) { completion in
            completion(nil, NSError(domain: "ImageIntakeTests", code: 1, userInfo: [NSLocalizedDescriptionKey: "Photo unavailable"]))
            return nil
        }
        try await expectRejected([provider])
    }

    func testRejectsAnimatedImageWithoutSavingADraft() async throws {
        let source = try XCTUnwrap(CGImageSourceCreateWithData(png() as CFData, nil))
        let animation = NSMutableData()
        let destination = try XCTUnwrap(CGImageDestinationCreateWithData(animation, UTType.gif.identifier as CFString, 2, nil))
        CGImageDestinationAddImageFromSource(destination, source, 0, nil)
        CGImageDestinationAddImageFromSource(destination, source, 0, nil)
        XCTAssertTrue(CGImageDestinationFinalize(destination))
        try await expectRejected([imageProvider(animation as Data, type: .gif)])
    }

    func testRejectsOversizedFileRepresentationBeforeImageDecode() async throws {
        let file = directory.appendingPathComponent("oversized.png")
        XCTAssertTrue(FileManager.default.createFile(atPath: file.path, contents: nil))
        let handle = try FileHandle(forWritingTo: file)
        try handle.truncate(atOffset: UInt64(DeviceStorage.maxCaptureImageBytes + 1))
        try handle.close()

        let provider = NSItemProvider()
        provider.registerFileRepresentation(forTypeIdentifier: UTType.png.identifier, fileOptions: [], visibility: .all) { completion in
            completion(file, false, nil)
            return nil
        }
        try await expectRejected([provider], containing: "too large")
    }

    private func expectRejected(_ providers: [NSItemProvider], containing message: String? = nil) async throws {
        do {
            _ = try await ImageIntake.persist(providers: providers, store: store, destination: nil)
            XCTFail("Unsupported input must not be accepted")
        } catch {
            if let message { XCTAssertTrue(error.localizedDescription.contains(message), error.localizedDescription) }
        }
        XCTAssertTrue(try store.list().isEmpty)
        let images = try FileManager.default.contentsOfDirectory(at: store.root, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "image" }
        XCTAssertTrue(images.isEmpty)
    }

    private func imageProvider(_ bytes: Data, type: UTType = .png) -> NSItemProvider {
        let provider = NSItemProvider()
        provider.registerDataRepresentation(forTypeIdentifier: type.identifier, visibility: .all) { completion in
            completion(bytes, nil)
            return nil
        }
        return provider
    }

    private func png() -> Data {
        UIGraphicsImageRenderer(size: CGSize(width: 2, height: 2)).pngData { context in
            UIColor.systemPurple.setFill()
            context.fill(CGRect(x: 0, y: 0, width: 2, height: 2))
        }
    }
}

/// Keeps Foundation's real file representation loading, observing only the
/// consumer callback boundary to prove persistence precedes file expiry.
private final class ObservedItemProvider: NSItemProvider, @unchecked Sendable {
    var afterConsumer: (@Sendable (URL?) -> Void)?

    override func loadFileRepresentation(forTypeIdentifier typeIdentifier: String,
                                        completionHandler: @escaping @Sendable (URL?, Error?) -> Void) -> Progress {
        let observer = afterConsumer
        return super.loadFileRepresentation(forTypeIdentifier: typeIdentifier) { url, error in
            completionHandler(url, error)
            observer?(url)
        }
    }
}
