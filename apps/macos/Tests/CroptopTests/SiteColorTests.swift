import XCTest
@testable import Croptop

final class SiteColorTests: XCTestCase {
    func testHexColorsMatchTemplateFormat() {
        XCTAssertEqual(SiteColor.hex(" #f8e\n"), 0xFF88EE)
        XCTAssertEqual(SiteColor.hex("#24211d"), 0x24211D)
        for value in ["", "auto", "red", "FFF8ED", "#1234", "#12345678", "#12345g", "#fff; color:red"] {
            XCTAssertNil(SiteColor.hex(value), value)
        }
    }

    func testMissingCounterpartStaysReadable() {
        XCTAssertEqual(SiteColor.contrasting(0xFFFFFF), 0x000000)
        XCTAssertEqual(SiteColor.contrasting(0xFFF8ED), 0x000000)
        XCTAssertEqual(SiteColor.contrasting(0x171717), 0xFFFFFF)
        XCTAssertEqual(SiteColor.contrasting(0x000000), 0xFFFFFF)
    }
}
