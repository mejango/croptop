import XCTest
@testable import Croptop

@MainActor
final class FirstLaunchSetupTests: XCTestCase {
    private func site(_ id: String = "11111111-1111-1111-1111-111111111111") throws -> Site {
        try JSONDecoder().decode(Site.self, from: JSONSerialization.data(withJSONObject: ["id": id, "name": "Untitled", "ipns": "k51test"]))
    }
    private func preferences() -> UserDefaults { UserDefaults(suiteName: "onboarding-test-" + UUID().uuidString)! }

    func testFreshInstallCreatesOneSiteAndFollowsOnceAcrossRestarts() async throws {
        let prefs = preferences(); var list: [Site] = []; var creates = 0; var follows = 0
        let starter = try site()
        func setup() -> FirstLaunchSetup {
            FirstLaunchSetup(preferences: prefs, scope: "fresh", sites: { list }, following: { [] },
                create: { creates += 1; list.append(starter); return starter }, follow: { follows += 1 })
        }
        let first = setup()
        let id = try await first.prepare(); XCTAssertEqual(id, starter.id)
        // Restart before the welcome feed finishes.
        let resumed = setup()
        let resumedID = try await resumed.prepare(); XCTAssertEqual(resumedID, starter.id)
        try await resumed.finishFollowing()
        let afterCompletion = try await setup().prepare(); XCTAssertNil(afterCompletion)
        try await setup().finishFollowing()
        XCTAssertEqual(creates, 1); XCTAssertEqual(follows, 1)
    }

    func testExistingUsersAreNotChangedEvenIfTheyLaterDeleteTheirSites() async throws {
        let prefs = preferences(); var list = [try site()]; var changes = 0
        let setup = FirstLaunchSetup(preferences: prefs, scope: "existing", sites: { list }, following: { [] },
            create: { changes += 1; return try self.site() }, follow: { changes += 1 })
        let id = try await setup.prepare(); XCTAssertNil(id)
        list = []
        let next = try await setup.prepare(); XCTAssertNil(next)
        try await setup.finishFollowing()
        XCTAssertEqual(changes, 0)
    }

    func testFailedCreateResponseDoesNotDuplicatePersistedSite() async throws {
        var list: [Site] = []; var attempts = 0; let starter = try site()
        let setup = FirstLaunchSetup(preferences: preferences(), scope: "interrupted", sites: { list }, following: { [] },
            create: { attempts += 1; list.append(starter); throw URLError(.timedOut) }, follow: {})
        do { _ = try await setup.prepare(); XCTFail("Expected interrupted response") } catch {}
        let id = try await setup.prepare(); XCTAssertEqual(id, starter.id)
        XCTAssertEqual(attempts, 1)
    }

    func testFollowingFailureRetainsSiteAndRetriesWithoutRecreatingIt() async throws {
        var list: [Site] = []; var follows = 0; var creates = 0; let starter = try site()
        let setup = FirstLaunchSetup(preferences: preferences(), scope: "offline", sites: { list }, following: { [] },
            create: { creates += 1; list.append(starter); return starter },
            follow: { follows += 1; if follows == 1 { throw URLError(.notConnectedToInternet) } })
        _ = try await setup.prepare()
        do { try await setup.finishFollowing(); XCTFail("Expected offline failure") } catch {}
        _ = try await setup.prepare(); try await setup.finishFollowing()
        XCTAssertEqual(creates, 1); XCTAssertEqual(follows, 2)
    }

    func testReadFailureDoesNotInitializeOrCreateAnything() async throws {
        var creates = 0
        let setup = FirstLaunchSetup(preferences: preferences(), scope: "unavailable", sites: { throw URLError(.cannotConnectToHost) }, following: { [] },
            create: { creates += 1; return try self.site() }, follow: { XCTFail("Must not follow") })
        do { _ = try await setup.prepare(); XCTFail("Expected read failure") } catch {}
        try await setup.finishFollowing(); XCTAssertEqual(creates, 0)
    }
}
