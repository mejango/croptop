import XCTest

final class CroptopUITests: XCTestCase {
    func testNativeHomeConnectionAndPrivacyScreens() {
        let app = XCUIApplication(); app.launch()
        XCTAssertTrue(app.buttons["Choose a screenshot"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS 'Shared draft storage is unavailable'")).firstMatch.exists)
        capture(app, name: "Native home")
        app.buttons["Connect your site"].tap()
        XCTAssertTrue(app.navigationBars["Connect your site"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.secureTextFields["Connection link"].exists)
        capture(app, name: "Connect phone")
        app.buttons["Done"].tap()
        app.swipeUp()
        app.buttons["Privacy and your content"].tap()
        XCTAssertTrue(app.navigationBars["Privacy"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["What leaves this phone"].exists)
        capture(app, name: "Privacy information")
    }

    func testScreenshotPickerDraftSurvivesRelaunch() {
        // The task-owned simulator is seeded with the repository's public icon
        // through simctl addmedia. No personal Photos library is used.
        let app = XCUIApplication(); app.launch()
        app.buttons["Choose a screenshot"].tap()
        let photo = app.images.matching(NSPredicate(format: "label BEGINSWITH 'Photo,'")).firstMatch
        XCTAssertTrue(photo.waitForExistence(timeout: 10), app.debugDescription)
        // The system photo picker's remote accessibility element can report
        // non-hittable despite its visible frame; tap its observed center.
        photo.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        XCTAssertTrue(app.navigationBars["New post"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.images["Post image preview"].exists)
        let caption = app.descendants(matching: .any)["Optional caption"]
        XCTAssertTrue(caption.waitForExistence(timeout: 5))
        let captionText = "Saved before connection " + UUID().uuidString.prefix(8)
        caption.tap(); caption.typeText(captionText)
        XCTAssertEqual(caption.value as? String, captionText)
        // Editing persists immediately; exercise termination without relying
        // on an explicit Save button or orderly sheet dismissal.
        app.terminate(); app.launch()
        let retained = app.buttons.containing(NSPredicate(format: "label CONTAINS %@", captionText)).firstMatch
        XCTAssertTrue(retained.waitForExistence(timeout: 10))
        retained.tap()
        XCTAssertEqual(app.descendants(matching: .any)["Optional caption"].value as? String, captionText)
        XCTAssertTrue(app.images["Post image preview"].exists)
        capture(app, name: "Retained screenshot draft")
    }

    private func capture(_ app: XCUIApplication, name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name; attachment.lifetime = .keepAlways
        add(attachment)
    }
}
