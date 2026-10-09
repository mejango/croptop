import Foundation
import XCTest
@testable import CroptopMobileCore

final class ConnectionLifecycleTests: XCTestCase {
    private var validLink: String {
        MobileEnvironment.productionOrigin.absoluteString + "/#pair=" + String(repeating: "A", count: 43) + "." + String(repeating: "B", count: 42) + "A"
    }

    func testIncomingLinkIsExactAndNeverDescribesCapability() throws {
        let link = try PairingLink(validLink)
        XCTAssertEqual(link.origin, MobileEnvironment.productionOrigin)
        XCTAssertFalse(String(describing: link).contains(link.capability))
        XCTAssertFalse(String(reflecting: link).contains(link.capability))
        let origin = MobileEnvironment.productionOrigin.absoluteString
        for invalid in [
            validLink.replacingOccurrences(of: origin, with: "https://app.crop.top"),
            validLink.replacingOccurrences(of: origin, with: "http://localhost:8080"),
            validLink.replacingOccurrences(of: "https://", with: "https://user@"),
            validLink.replacingOccurrences(of: "/#", with: ":443/#"),
            validLink.replacingOccurrences(of: "/#", with: "/other#"),
            validLink.replacingOccurrences(of: "/#", with: "/?tracking=1#"),
            validLink.replacingOccurrences(of: "#pair=", with: "#pair%3D"),
            validLink + "&extra=1", validLink + "=", validLink + "\n",
            String(validLink.dropLast()) + "B"
        ] { XCTAssertThrowsError(try PairingLink(invalid), "Accepted noncanonical connection link") }
    }

    func testIncomingLinksCannotReplaceWorkConnectionOrPairing() throws {
        XCTAssertNoThrow(try ConnectionActionPolicy.requireNewPairing(isBusy: false, hasConnection: false, hasPairing: false))
        for (busy, connected, pairing) in [(true, false, false), (false, true, false), (false, false, true)] {
            XCTAssertThrowsError(try ConnectionActionPolicy.requireNewPairing(isBusy: busy, hasConnection: connected, hasPairing: pairing))
        }
        XCTAssertThrowsError(try ConnectionActionPolicy.requireConnectionChange(isBusy: true, hasPairing: false))
        XCTAssertThrowsError(try ConnectionActionPolicy.requireConnectionChange(isBusy: false, hasPairing: true))
    }

    func testPersistedConnectionRequiresExactApprovedOriginBeforeReuse() throws {
        XCTAssertNoThrow(try MobileEnvironment.requireApprovedOrigin(MobileEnvironment.productionOrigin))
        let origin = MobileEnvironment.productionOrigin.absoluteString
        for invalid in ["https://app.crop.top", "http://localhost:8080", "https://example.com", origin + "/", origin + ":443", origin + "?other=1"] {
            XCTAssertThrowsError(try MobileEnvironment.requireApprovedOrigin(XCTUnwrap(URL(string: invalid))))
        }
    }

    func testIndependentAppAndExtensionLeasePreventsRemovalOrSecondSigning() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let app = try DraftStore(root: root), share = try DraftStore(root: root)
        let connection = SiteConnection(origin: MobileEnvironment.productionOrigin, ipns: "site", name: "Retained")
        try app.setConnection(connection)
        var extensionCommit: MobileActivityLease? = try share.acquireActivityLease()
        XCTAssertNotNil(extensionCommit)
        XCTAssertThrowsError(try app.acquireActivityLease())
        // Capturing a screenshot stays available while network activity is held.
        let draft = try app.create(image: Data([1]), contentType: "image/png", destination: connection)
        XCTAssertEqual(try share.load(draft.id).destination, connection)
        extensionCommit = nil // also released automatically if the extension dies
        let removal = try app.acquireActivityLease()
        try app.setConnection(nil)
        XCTAssertThrowsError(try share.acquireActivityLease())
        withExtendedLifetime(removal) { XCTAssertNil(try? share.connection()) }
        XCTAssertEqual(try app.load(draft.id).destination, connection)
    }

    func testComposerIncomingLinkSetupDismissalReturnsToOriginalComposer() {
        var presentation = MobilePresentation()
        presentation.sheet = .composer
        presentation.openPairing(hasSelectedDraft: true)
        XCTAssertNil(presentation.sheet)
        XCTAssertFalse(presentation.mayPresentCapturedDraft)
        presentation.didDismiss() // old composer was dismissed
        XCTAssertEqual(presentation.sheet, .connection)
        XCTAssertFalse(presentation.mayPresentCapturedDraft) // late PhotosPicker result stays saved
        presentation.sheet = nil
        presentation.didDismiss() // setup finished or cancelled
        XCTAssertEqual(presentation.sheet, .composer)
        presentation.sheet = nil
        presentation.didDismiss()
        XCTAssertNil(presentation.sheet)
    }

    func testDelayedReplacementTargetsOriginalDraftOnly() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let store = try DraftStore(root: root)
        let original = try store.create(image: Data([1]), contentType: "image/png", destination: nil)
        let newlySelected = try store.create(image: Data([2]), contentType: "image/png", destination: nil)
        try store.replaceImage(Data([3]), contentType: "image/png", for: original.id)
        XCTAssertEqual(try Data(contentsOf: store.imageURL(original)), Data([3]))
        XCTAssertEqual(try Data(contentsOf: store.imageURL(newlySelected)), Data([2]))
    }
}
