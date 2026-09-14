import AppKit
import Combine
import SwiftUI
import XCTest
@testable import Croptop

final class SiteReorderingTests: XCTestCase {
    @MainActor private func withModel(_ body: (AppModel, UserDefaults) -> Void) {
        let suite = "CroptopTests.SiteReordering." + UUID().uuidString
        let preferences = UserDefaults(suiteName: suite)!
        defer { preferences.removePersistentDomain(forName: suite) }
        let model = AppModel(preferences: preferences)
        model.sites = ["a", "b", "c", "d"].map { Site(id: $0, name: $0.uppercased(), ipns: $0) }
        body(model, preferences)
    }

    @MainActor func testDropBeforeFirstRowAndAfterLastRow() {
        withModel { model, _ in
            XCTAssertTrue(model.moveSite("d", beforeRow: 0))
            XCTAssertEqual(model.sites.map(\.id), ["d", "a", "b", "c"])
            XCTAssertTrue(model.moveSite("d", beforeRow: model.sites.count))
            XCTAssertEqual(model.sites.map(\.id), ["a", "b", "c", "d"])
        }
    }

    @MainActor func testInsertionGapAccountsForRemovedRowWhenMovingDown() {
        withModel { model, _ in
            XCTAssertTrue(model.moveSite("a", beforeRow: 3))
            XCTAssertEqual(model.sites.map(\.id), ["b", "c", "a", "d"])
            XCTAssertTrue(model.moveSite("d", beforeRow: 1))
            XCTAssertEqual(model.sites.map(\.id), ["b", "d", "c", "a"])
        }
    }

    @MainActor func testAdjacentGapsInvalidRowsAndMissingSitesDoNotChangeOrderOrPreferences() {
        withModel { model, preferences in
            let before = preferences.dictionaryRepresentation().filter { $0.key.hasPrefix("siteOrder:") }
            for row in [1, 2, -1, 5] { XCTAssertFalse(model.moveSite("b", beforeRow: row)) }
            XCTAssertFalse(model.moveSite("missing", beforeRow: 0))
            XCTAssertEqual(model.sites.map(\.id), ["a", "b", "c", "d"])
            let after = preferences.dictionaryRepresentation().filter { $0.key.hasPrefix("siteOrder:") }
            XCTAssertEqual(NSDictionary(dictionary: after), NSDictionary(dictionary: before))
        }
    }

    @MainActor func testDropPersistsOneCompleteOrderAndPreservesOpenEditor() {
        withModel { model, preferences in
            model.screen = .editor(site: "a", post: "draft")
            var orders: [[String]] = []
            let subscription = model.$sites.sink { orders.append($0.map(\.id)) }
            XCTAssertTrue(model.moveSite("b", beforeRow: 4))
            XCTAssertEqual(orders, [["a", "b", "c", "d"], ["a", "c", "d", "b"]])
            XCTAssertEqual(model.screen, .editor(site: "a", post: "draft"))
            XCTAssertEqual(model.currentSiteID, "a")
            let reopened = AppModel(preferences: preferences)
            let incoming = ["b", "c", "new", "d", "a"].map { Site(id: $0, name: $0, ipns: $0) }
            XCTAssertEqual(reopened.orderedSites(incoming).map(\.id), ["a", "c", "d", "b", "new"])
            withExtendedLifetime(subscription) {}
        }
    }

    @MainActor func testSelectingARowToBeginDraggingDoesNotNavigateOrReorder() {
        withModel { model, preferences in
            model.screen = .settings("a")
            let coordinator = ReorderableSiteList.Coordinator(model: model)
            let table = NSTableView()
            table.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("site")))
            table.delegate = coordinator
            table.dataSource = coordinator
            coordinator.refresh(table)

            // Native row dragging first selects the source row. Only a completed
            // click opens it; preparing the drag must preserve the settings screen.
            table.selectRowIndexes(IndexSet(integer: 2), byExtendingSelection: false)
            // Unrelated updates must not reset native selection while the user
            // is pressing a row and has not crossed the drag threshold yet.
            coordinator.refresh(table)
            XCTAssertEqual(table.selectedRow, 2)
            let payload = coordinator.tableView(table, pasteboardWriterForRow: 2) as? NSPasteboardItem
            XCTAssertEqual(payload?.string(forType: ReorderableSiteList.Coordinator.pasteboardType), "c")
            XCTAssertEqual(model.screen, .settings("a"))
            XCTAssertEqual(model.sites.map(\.id), ["a", "b", "c", "d"])
            XCTAssertTrue(preferences.dictionaryRepresentation().keys.allSatisfy { !$0.hasPrefix("siteOrder:") })
            coordinator.refresh(table, restoreSelection: true)
            XCTAssertEqual(table.selectedRow, 0)
        }
    }

    @MainActor func testNativeDropMovesExistingRowsWithoutReloading() {
        withModel { model, _ in
            model.screen = .settings("a")
            let coordinator = ReorderableSiteList.Coordinator(model: model)
            let table = ReorderTrackingTable()
            table.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("site")))
            table.delegate = coordinator
            table.dataSource = coordinator
            coordinator.refresh(table)
            let initialReloads = table.reloadCount
            table.selectRowIndexes(IndexSet(integer: 1), byExtendingSelection: false)

            XCTAssertTrue(coordinator.moveSite("b", beforeRow: 4, in: table))
            coordinator.refresh(table, restoreSelection: true)
            XCTAssertEqual(model.sites.map(\.id), ["a", "c", "d", "b"])
            XCTAssertEqual(table.moves.map { [$0.source, $0.destination] }, [[1, 3]])
            XCTAssertEqual(table.reloadCount, initialReloads)
            XCTAssertEqual(table.selectedRow, 0)
            XCTAssertEqual(model.screen, .settings("a"))
            XCTAssertEqual(table.moveDuration, NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 0 : 0.18)

            for row in [3, 4, -1, 5] { XCTAssertFalse(coordinator.moveSite("b", beforeRow: row, in: table)) }
            XCTAssertFalse(coordinator.moveSite("missing", beforeRow: 0, in: table))
            XCTAssertEqual(table.moves.count, 1)
            XCTAssertEqual(table.reloadCount, initialReloads)
        }
    }

    @MainActor func testMetadataChangedDuringDragRefreshesAfterTheMove() {
        withModel { model, _ in
            let coordinator = ReorderableSiteList.Coordinator(model: model)
            let table = ReorderTrackingTable()
            table.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("site")))
            table.delegate = coordinator
            table.dataSource = coordinator
            coordinator.refresh(table)
            let initialReloads = table.reloadCount

            // A load can update a name or avatar revision while drag-time refresh
            // is deferred. Moving cached rows must not mark those views fresh.
            model.sites[2].name = "Updated site"
            model.sites[2].updated = 123
            XCTAssertTrue(coordinator.moveSite("b", beforeRow: 4, in: table))
            XCTAssertEqual(table.reloadCount, initialReloads)
            coordinator.refresh(table, restoreSelection: true)
            XCTAssertEqual(table.reloadCount, initialReloads + 1)
            XCTAssertEqual(model.sites[1].name, "Updated site")
            XCTAssertEqual(model.sites[1].updated, 123)
            coordinator.refresh(table)
            XCTAssertEqual(table.reloadCount, initialReloads + 1)
        }
    }

    @MainActor func testPublishingChangesRefreshRowsWithoutSiteOrSelectionChanges() {
        withModel { model, _ in
            model.screen = .site("a")
            let coordinator = ReorderableSiteList.Coordinator(model: model)
            let table = ReorderTrackingTable()
            table.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("site")))
            table.delegate = coordinator
            table.dataSource = coordinator
            coordinator.refresh(table)
            let initialReloads = table.reloadCount

            @MainActor func rowIsPublishing(_ row: Int) -> Bool? {
                let cell = coordinator.tableView(table, viewFor: table.tableColumns[0], row: row)
                return (cell?.subviews.first as? NSHostingView<RailItemContent>)?.rootView.publishing
            }

            XCTAssertEqual(rowIsPublishing(2), false)
            model.publishing.insert("c")
            coordinator.refresh(table)
            XCTAssertEqual(table.reloadCount, initialReloads + 1)
            XCTAssertEqual(rowIsPublishing(2), true)
            XCTAssertEqual(rowIsPublishing(0), false)
            XCTAssertEqual(table.selectedRow, 0)

            model.publishing.insert("a")
            coordinator.refresh(table)
            XCTAssertEqual(rowIsPublishing(0), true)
            XCTAssertEqual(rowIsPublishing(2), true)
            model.publishing.remove("c")
            coordinator.refresh(table)
            XCTAssertEqual(rowIsPublishing(2), false)
            XCTAssertEqual(rowIsPublishing(0), true)
            model.publishing.remove("a")
            coordinator.refresh(table)
            XCTAssertEqual(rowIsPublishing(0), false)
            let completedReloads = table.reloadCount
            coordinator.refresh(table)
            XCTAssertEqual(table.reloadCount, completedReloads)
            XCTAssertEqual(model.screen, .site("a"))
        }
    }

    @MainActor func testSiteMenuMatchesHeaderActionsAndTargetsClickedSite() {
        withModel { model, _ in
            model.screen = .site("a")
            let coordinator = ReorderableSiteList.Coordinator(model: model)
            let table = NSTableView()
            table.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("site")))
            table.delegate = coordinator
            table.dataSource = coordinator
            coordinator.refresh(table)

            guard let menu = coordinator.menu(for: 2) else { return XCTFail("Missing site menu") }
            XCTAssertEqual(menu.items.map(\.title), ["Website", "Settings", "Publish"])
            XCTAssertTrue(menu.items.allSatisfy { ($0.representedObject as? String) == "c" })
            let settings = menu.items[1]
            guard let action = settings.action else { return XCTFail("Missing Settings action") }
            // The command-line test runner does not create the NSApplication
            // dispatcher that a running SwiftUI app provides for menu actions.
            XCTAssertTrue(NSApplication.shared.sendAction(action, to: settings.target, from: settings))
            XCTAssertEqual(model.screen, .settings("c"))
            XCTAssertEqual(model.sites.map(\.id), ["a", "b", "c", "d"])

            model.publishing.insert("c")
            let publishingMenu = coordinator.menu(for: 2)
            XCTAssertEqual(publishingMenu?.items.last?.title, "Publishing…")
            XCTAssertEqual(publishingMenu?.items.last?.isEnabled, false)
            XCTAssertEqual(coordinator.menu(for: 0)?.items.last?.isEnabled, true)
        }
    }
}

@MainActor private final class ReorderTrackingTable: NSTableView {
    var reloadCount = 0
    var moves: [(source: Int, destination: Int)] = []
    var moveDuration: TimeInterval?

    override func reloadData() {
        reloadCount += 1
        super.reloadData()
    }
    override func moveRow(at oldIndex: Int, to newIndex: Int) {
        moves.append((oldIndex, newIndex))
        moveDuration = NSAnimationContext.current.duration
        super.moveRow(at: oldIndex, to: newIndex)
    }
}
