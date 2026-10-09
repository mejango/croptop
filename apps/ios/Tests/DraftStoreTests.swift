import XCTest
@testable import CroptopMobileCore

final class DraftStoreTests: XCTestCase {
    private var directory: URL!
    private var store: DraftStore!
    private let connection = SiteConnection(origin: URL(string: "https://app.crop.top")!, ipns: "k51-test-site", name: "Test site")

    override func setUpWithError() throws {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("croptop-draft-test-\(UUID().uuidString)")
        store = try DraftStore(root: directory)
    }

    override func tearDownWithError() throws { try FileManager.default.removeItem(at: directory) }

    func testShareBeforeSetupSurvivesReopenAndRequiresDestination() throws {
        let draft = try store.create(image: Data([1, 2, 3]), contentType: "image/png", destination: nil)
        let reopened = try DraftStore(root: directory)
        XCTAssertEqual(try reopened.list().first?.id, draft.id)
        XCTAssertEqual(try Data(contentsOf: reopened.imageURL(draft)), Data([1, 2, 3]))
        XCTAssertThrowsError(try reopened.beginSubmission(draft.id))
        _ = try reopened.edit(draft.id, title: "A screenshot", caption: "Hello", destination: connection)
        let pending = try reopened.beginSubmission(draft.id)
        XCTAssertEqual(pending.destination, connection)
        XCTAssertEqual(pending.statusLabel, "Pending — check status")
    }

    func testLostUploadResponseLocksInputAndRetainsID() throws {
        let draft = try store.create(image: Data([4]), contentType: "image/png", destination: connection)
        _ = try store.edit(draft.id, title: "Title", caption: "Caption", destination: connection)
        _ = try store.beginSubmission(draft.id)
        let reopened = try DraftStore(root: directory)
        XCTAssertThrowsError(try reopened.edit(draft.id, title: "Replacement", caption: "", destination: connection))
        let retried = try reopened.beginSubmission(draft.id)
        XCTAssertEqual(retried.id, draft.id)
        XCTAssertEqual(retried.title, "Title")
        XCTAssertEqual(retried.caption, "Caption")
    }

    func testSiteSwitchCannotRetargetExistingDraft() throws {
        let draft = try store.create(image: Data([4]), contentType: "image/png", destination: connection)
        let other = SiteConnection(origin: connection.origin, ipns: "k51-other", name: "Other")
        try store.setConnection(other)
        XCTAssertThrowsError(try store.edit(draft.id, title: "", caption: "", destination: other))
        XCTAssertEqual(try store.load(draft.id).destination, connection)
    }

    func testConfirmedPostCannotRegressOnLateResponse() throws {
        let draft = try store.create(image: Data([4]), contentType: "image/png", destination: connection)
        _ = try store.beginSubmission(draft.id)
        let published = operation(draft, state: .published, url: "https://crop.top/test/\(draft.id)")
        _ = try store.record(published, for: draft.id)
        _ = try store.record(operation(draft, state: .committing), for: draft.id)
        XCTAssertEqual(try store.load(draft.id).operation?.state, .published)
        XCTAssertEqual(try store.load(draft.id).operation?.url, published.url)
    }

    func testPublishedNeedsUsableLinkAndMatchingIdentity() throws {
        let draft = try store.create(image: Data([4]), contentType: "image/png", destination: connection)
        XCTAssertThrowsError(try store.record(operation(draft, state: .published), for: draft.id))
        let otherDraft = try store.create(image: Data([4]), contentType: "image/png", destination: connection)
        XCTAssertThrowsError(try store.record(operation(otherDraft, state: .preparing), for: draft.id))
        XCTAssertThrowsError(try store.load("../connection"))
    }

    func testFreshIdentityRequiresDefinitiveExpiryAndRetainsOriginal() throws {
        let draft = try store.create(image: Data([7, 8]), contentType: "image/png", destination: connection)
        _ = try store.edit(draft.id, title: "Retained title", caption: "Retained caption", destination: connection)
        _ = try store.beginSubmission(draft.id)
        try store.noteError("Network unavailable", for: draft.id)
        XCTAssertThrowsError(try store.startNewAfterRejection(draft.id))
        try store.noteError("Upload expired", code: "draft_expired", for: draft.id)
        let replacement = try store.startNewAfterRejection(draft.id)
        XCTAssertNotEqual(replacement.id, draft.id)
        XCTAssertEqual(replacement.title, "Retained title")
        XCTAssertEqual(replacement.caption, "Retained caption")
        XCTAssertEqual(replacement.destination, connection)
        XCTAssertFalse(replacement.submitted)
        XCTAssertEqual(try Data(contentsOf: store.imageURL(replacement)), Data([7, 8]))
        XCTAssertTrue(try store.load(draft.id).submitted)
        XCTAssertEqual(try store.list().count, 2)
    }

    func testValidationFailureLeavesDraftEditable() throws {
        let draft = try store.create(image: Data([1]), contentType: "image/png", destination: connection)
        XCTAssertThrowsError(try store.beginSubmissionAttempt(draft.id) { _, _ in throw MobileError.invalid("Shorten caption") })
        XCTAssertFalse(try store.load(draft.id).submitted)
        _ = try store.edit(draft.id, title: "Fixed", caption: "", destination: connection)
        let first = try store.beginSubmissionAttempt(draft.id)
        let second = try store.beginSubmissionAttempt(draft.id)
        XCTAssertFalse(first.wasSubmitted)
        XCTAssertTrue(second.wasSubmitted)
        XCTAssertEqual(first.image, second.image)
    }

    func testCommitIntentSurvivesLostResponseAndReconcilesSameOperation() throws {
        let draft = try store.create(image: Data([1]), contentType: "image/png", destination: connection)
        _ = try store.beginSubmission(draft.id)
        let proposal = MobileProposal(id: "proposal", cid: "cid", parent: "parent", sequence: "1", host: "crop.top", time: 1, expiresAt: 2, recordPayload: "", pushPayload: "")
        let ready = MobileOperation(id: draft.id, ipns: connection.ipns, postID: draft.id, state: .needsSignature,
            title: draft.title, caption: draft.caption, createdAt: 1, mediaSHA256: "", mediaType: "image/png", proposal: proposal, url: nil, error: nil, code: nil)
        _ = try store.record(ready, for: draft.id)
        try store.beginCommit(draft.id, proposalID: "proposal")
        let reopened = try DraftStore(root: directory)
        XCTAssertTrue(try reopened.load(draft.id).commitIsUnconfirmed)
        XCTAssertEqual(try reopened.load(draft.id).statusLabel, "Confirming publication")
        XCTAssertThrowsError(try reopened.startNewAfterRejection(draft.id))
        _ = try reopened.record(operation(draft, state: .published, url: "https://crop.top/post"), for: draft.id)
        XCTAssertFalse(try reopened.load(draft.id).commitIsUnconfirmed)
        XCTAssertEqual(try reopened.load(draft.id).statusLabel, "Published")
    }

    func testUploadLimitsUseUTF8BytesFromServiceConfiguration() throws {
        let configuration = ServiceConfiguration(version: 1, enabled: true, origin: "https://app.crop.top", host: "crop.top",
            maxImageBytes: 1024, maxImagePixels: 1000, maxTitleBytes: 4, maxCaptionBytes: 8, formats: ["image/png"], maxImages: 1)
        var draft = try store.create(image: Data([1]), contentType: "image/png", destination: connection)
        draft.title = "🌱"; draft.caption = "🌱🌱"
        XCTAssertNoThrow(try MobileAPI.validateUpload(draft, image: Data([1]), configuration: configuration))
        draft.title += "x"
        XCTAssertThrowsError(try MobileAPI.validateUpload(draft, image: Data([1]), configuration: configuration))
        draft.title = ""; draft.caption += "x"
        XCTAssertThrowsError(try MobileAPI.validateUpload(draft, image: Data([1]), configuration: configuration))
    }

    private func operation(_ draft: LocalDraft, state: OperationState, url: String? = nil) -> MobileOperation {
        MobileOperation(id: draft.id, ipns: connection.ipns, postID: draft.id, state: state,
                        title: draft.title, caption: draft.caption, createdAt: 1, mediaSHA256: "",
                        mediaType: "image/png", proposal: nil, url: url, error: nil, code: nil)
    }
}
