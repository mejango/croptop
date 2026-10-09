import XCTest
@testable import Croptop

final class EngineStartupTests: XCTestCase {
    @MainActor func testLegacyServiceStopsBeforeBundledEngineLaunches() async throws {
        var events: [String] = []
        var running = true
        let launch = try await EngineStartup.needsLaunch(externalConsole: false, bundled: true, legacyService: true,
            retire: { events.append("retire") },
            ping: { events.append("ping"); return running },
            pause: { events.append("wait"); running = false })
        XCTAssertTrue(launch)
        XCTAssertEqual(events, ["retire", "ping", "wait", "ping"])
    }
    @MainActor func testExternalConsoleNeverMigratesOrLaunches() async throws {
        let launch = try await EngineStartup.needsLaunch(externalConsole: true, bundled: true, legacyService: true,
            retire: { XCTFail("Must not retire external console") },
            ping: { XCTFail("External startup is caller-managed"); return false },
            pause: { XCTFail("Must not wait") })
        XCTAssertFalse(launch)
    }
    @MainActor func testAnotherAppProcessIsReusedWithoutTermination() async throws {
        let launch = try await EngineStartup.needsLaunch(externalConsole: false, bundled: true, legacyService: false,
            retire: { XCTFail("No login service to retire") }, ping: { true }, pause: {})
        XCTAssertFalse(launch)
    }
    @MainActor func testStuckLegacyProcessPreventsSecondEngine() async {
        var waits = 0
        do {
            _ = try await EngineStartup.needsLaunch(externalConsole: false, bundled: true, legacyService: true,
                retire: {}, ping: { true }, pause: { waits += 1 })
            XCTFail("Must not expose the old engine or start a second one")
        } catch { XCTAssertEqual(waits, 40) }
    }
    @MainActor func testNormalRelaunchStartsBundledEngine() async throws {
        let launch = try await EngineStartup.needsLaunch(externalConsole: false, bundled: true, legacyService: false,
            retire: { XCTFail("Migration already complete") }, ping: { false }, pause: {})
        XCTAssertTrue(launch)
    }
}
