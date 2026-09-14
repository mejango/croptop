import XCTest
@testable import Croptop

final class AppUpdateTests: XCTestCase {
    func testCurrentAppIgnoresOlderEngineUpdateFlag() async {
        await MainActor.run {
            let model = AppModel(appVersion: "0.11.0")
            model.status = Status(version: "0.9.1", latest: "0.11.0", update: true,
                                  ipfs: .init(running: true, peers: 1))
            XCTAssertNil(model.availableAppUpdate)
            XCTAssertEqual(model.appVersion, "0.11.0")
        }
    }

    func testEngineCannotInventAnAppUpdate() async {
        await MainActor.run {
            let model = AppModel(appVersion: "0.10.0")
            model.status = Status(version: "0.11.0", latest: "0.11.0", update: false,
                                  ipfs: .init(running: true, peers: 1))
            XCTAssertNil(model.availableAppUpdate)
            model.status?.latest = "0.12.0"
            XCTAssertNil(model.availableAppUpdate)
        }
    }

    func testReleaseOrderingAndUnknownVersions() {
        XCTAssertEqual(AppVersion.newerRelease(installed: "0.9.9", latest: "v0.11.0"), "v0.11.0")
        XCTAssertNil(AppVersion.newerRelease(installed: "0.11.0", latest: "0.9.9"))
        XCTAssertNil(AppVersion.newerRelease(installed: "0.11.0+local", latest: "0.11.0"))
        for version: String? in [nil, "", "dev", "garbage", "0..1", "0.11"] {
            XCTAssertNil(AppVersion.newerRelease(installed: version, latest: "0.11.0"))
            XCTAssertNil(AppVersion.newerRelease(installed: "0.11.0", latest: version))
        }
    }
    func testInstallWaitsForWorkAndResumesOnlyOnce() async {
        await MainActor.run {
            let gate = UpdateRelaunchGate()
            var installs = 0
            XCTAssertFalse(gate.postpone { installs += 1 })
            gate.blocked = true
            XCTAssertTrue(gate.postpone { installs += 1 })
            XCTAssertEqual(installs, 0)
            gate.blocked = false
            gate.blocked = false
            XCTAssertEqual(installs, 1)
            gate.blocked = true
            XCTAssertTrue(gate.postpone { installs += 1 })
            gate.cancel()
            gate.blocked = false
            XCTAssertEqual(installs, 1)
        }
    }

    func testEditingAndPublishingBlockRelaunch() async {
        await MainActor.run {
            for screen: Screen in [.editor(site: "a", post: nil), .quick("a"), .settings("a")] {
                XCTAssertTrue(AppUpdater.blocksRelaunch(screen: screen, sheet: nil, publishing: []))
            }
            XCTAssertTrue(AppUpdater.blocksRelaunch(screen: .feed, sheet: .newSite, publishing: []))
            XCTAssertTrue(AppUpdater.blocksRelaunch(screen: .site("a"), sheet: nil, publishing: ["a"]))
            XCTAssertFalse(AppUpdater.blocksRelaunch(screen: .site("a"), sheet: nil, publishing: []))
        }
    }

    func testSiteOrderPersistsAndNewSitesStayVisible() async {
        await MainActor.run {
            let suite = "CroptopTests." + UUID().uuidString
            let preferences = UserDefaults(suiteName: suite)!
            defer { preferences.removePersistentDomain(forName: suite) }
            let a = Site(id: "a", name: "A", ipns: "a")
            let b = Site(id: "b", name: "B", ipns: "b")
            let c = Site(id: "c", name: "C", ipns: "c")
            let model = AppModel(preferences: preferences)
            model.sites = [a, b, c]
            model.moveSite("c", onto: "a")
            XCTAssertEqual(model.sites.map(\.id), ["c", "a", "b"])
            let reopened = AppModel(preferences: preferences)
            XCTAssertEqual(reopened.orderedSites([a, b, c]).map(\.id), ["c", "a", "b"])
            let d = Site(id: "d", name: "D", ipns: "d")
            XCTAssertEqual(reopened.orderedSites([a, d, c]).map(\.id), ["c", "a", "d"])
            model.moveSite("c", by: -1)
            XCTAssertEqual(model.sites.map(\.id), ["c", "a", "b"])
            model.moveSite("c", by: 1)
            XCTAssertEqual(model.sites.map(\.id), ["a", "c", "b"])
        }
    }

    func testPreviewEscapesTitleAndDisablesActiveContent() {
        let html = PreviewDocument.html(title: "<img src=x onerror=alert(1)>", body: "<p>Body</p>")
        XCTAssertTrue(html.contains("&lt;img"))
        XCTAssertFalse(html.contains("<h1><img"))
        XCTAssertTrue(html.contains("default-src 'none'"))
        XCTAssertTrue(html.contains("form-action 'none'"))
        XCTAssertTrue(html.contains("<p>Body</p>"))
    }

    func testMultipartClosingIsIdempotent() {
        let form = Multipart()
        form.field("name", "A site")
        form.file("avatar", filename: "logo.png", data: Data([1, 2, 3]))
        form.close()
        let body = form.body
        form.close()
        XCTAssertEqual(form.body, body)
        let boundary = form.contentType.components(separatedBy: "boundary=")[1]
        XCTAssertTrue(body.suffix(Data("--\(boundary)--\r\n".utf8).count) == Data("--\(boundary)--\r\n".utf8))
    }

}
