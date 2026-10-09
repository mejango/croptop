import XCTest
import SwiftUI
@testable import Croptop

final class SitePostLayoutTests: XCTestCase {
    func testColumnCountsAdaptToAvailableWidth() {
        XCTAssertEqual(SitePostViewMode.tiles.columnCount(for: 259), 1)
        XCTAssertEqual(SitePostViewMode.tiles.columnCount(for: 539), 1)
        XCTAssertEqual(SitePostViewMode.tiles.columnCount(for: 540), 2)
        XCTAssertEqual(SitePostViewMode.tiles.columnCount(for: 819), 2)
        XCTAssertEqual(SitePostViewMode.tiles.columnCount(for: 820), 3)
        XCTAssertEqual(SitePostViewMode.tiles.columnCount(for: 2_000), 3)

        XCTAssertEqual(SitePostViewMode.more.columnCount(for: 315), 1)
        XCTAssertEqual(SitePostViewMode.more.columnCount(for: 316), 2)
        XCTAssertEqual(SitePostViewMode.more.columnCount(for: 814), 5)
        XCTAssertEqual(SitePostViewMode.more.columnCount(for: 2_000), 5)
    }

    func testMoreFitsMorePostsWhileListAlwaysHasOneColumn() {
        for width: CGFloat in [540, 820, 1_280, 1_800] {
            XCTAssertGreaterThan(SitePostViewMode.more.columnCount(for: width), SitePostViewMode.tiles.columnCount(for: width))
            XCTAssertEqual(SitePostViewMode.list.columnCount(for: width), 1)
        }
        XCTAssertEqual(SitePostViewMode.tiles.spacing, 20)
        XCTAssertEqual(SitePostViewMode.more.spacing, 16)
        XCTAssertEqual(SitePostViewMode.list.spacing, 0)
    }

    func testNarrowAndUnmeasuredWidthsHaveOneColumn() {
        for mode in SitePostViewMode.allCases {
            for width: CGFloat in [-1, 0, 100, .infinity, .nan] {
                XCTAssertEqual(mode.columnCount(for: width), 1)
            }
        }
    }

    func testColumnsIncludeNewPostTileAndPreserveReadingOrder() {
        let items = ["new", "first", "second", "third", "fourth", "fifth", "sixth", "seventh"]
        let columns = SitePostColumns.distribute(items, count: 3)
        XCTAssertEqual(columns, [["new", "third", "sixth"], ["first", "fourth", "seventh"], ["second", "fifth"]])
        let rows = columns.map(\.count).max() ?? 0
        let readingOrder = (0..<rows).flatMap { row in
            columns.compactMap { column in row < column.count ? column[row] : nil }
        }
        XCTAssertEqual(readingOrder, items)
    }

    func testEmptyAndSingleColumnDistribution() {
        XCTAssertEqual(SitePostColumns.distribute([Int](), count: 3), [[], [], []])
        XCTAssertEqual(SitePostColumns.distribute([1, 2, 3], count: 1), [[1, 2, 3]])
        XCTAssertEqual(SitePostColumns.distribute([1, 2], count: 4), [[1], [2], [], []])
        XCTAssertEqual(SitePostColumns.distribute([1, 2], count: 0), [[1, 2]])
    }
}
