import AppKit
import SwiftUI

// AppKit owns row gestures so dragging works from the avatar, text or empty space.
// Its insertion gap previews the move; the model is changed only on a valid drop.
struct ReorderableSiteList: NSViewRepresentable {
    @ObservedObject var model: AppModel

    func makeCoordinator() -> Coordinator { Coordinator(model: model) }

    func makeNSView(context: Context) -> NSScrollView {
        let table = SiteOrderTableView()
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("site"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.rowHeight = 56
        table.intercellSpacing = .zero
        table.backgroundColor = NSColor(Theme.paper)
        table.selectionHighlightStyle = .none
        table.focusRingType = .none
        table.allowsMultipleSelection = false
        table.allowsEmptySelection = true
        table.columnAutoresizingStyle = .lastColumnOnlyAutoresizingStyle
        table.draggingDestinationFeedbackStyle = .gap
        table.registerForDraggedTypes([Coordinator.pasteboardType])
        table.setDraggingSourceOperationMask(.move, forLocal: true)
        table.setDraggingSourceOperationMask([], forLocal: false)
        table.dataSource = context.coordinator
        table.delegate = context.coordinator
        table.target = context.coordinator
        table.action = #selector(Coordinator.openClickedSite(_:))
        table.menuForRow = { [weak coordinator = context.coordinator] row in coordinator?.menu(for: row) }
        table.openKeyboardSelection = { [weak coordinator = context.coordinator] row in coordinator?.openSite(at: row) }
        table.setAccessibilityLabel("Your sites")

        let scroll = NSScrollView()
        scroll.borderType = .noBorder
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.scrollerStyle = .overlay
        scroll.documentView = table
        context.coordinator.refresh(table)
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        guard let table = scroll.documentView as? NSTableView else { return }
        context.coordinator.refresh(table)
    }

    @MainActor final class Coordinator: NSObject, NSTableViewDataSource, NSTableViewDelegate {
        static let pasteboardType = NSPasteboard.PasteboardType("top.crop.croptop.site-order")
        let model: AppModel
        private var sites: [Site] = []
        private var currentSiteID: String?
        private var publishing: Set<String> = []
        private var draggingID: String?

        init(model: AppModel) { self.model = model }

        func refresh(_ table: NSTableView, restoreSelection: Bool = false) {
            guard draggingID == nil else { return }
            let changed = sites != model.sites || currentSiteID != model.currentSiteID || publishing != model.publishing
            guard changed || restoreSelection else { return }
            sites = model.sites
            currentSiteID = model.currentSiteID
            publishing = model.publishing
            if changed { table.reloadData() }
            let selected = sites.firstIndex { $0.id == currentSiteID }
            table.selectRowIndexes(selected.map { IndexSet(integer: $0) } ?? [], byExtendingSelection: false)
        }

        func numberOfRows(in tableView: NSTableView) -> Int { sites.count }

        func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
            guard sites.indices.contains(row) else { return nil }
            let site = sites[row]
            let content = RailItemContent(title: site.name, second: site.secondLine,
                                          current: currentSiteID == site.id,
                                          logoURL: API.shared.siteFile(site.id, "avatar.png"),
                                          logoRevision: site.updated.map { String($0) },
                                          collaborative: site.isCollaborative,
                                          publishing: publishing.contains(site.id))
            let identifier = NSUserInterfaceItemIdentifier("site-row")
            if let cell = tableView.makeView(withIdentifier: identifier, owner: nil) as? SiteOrderCell {
                cell.host.rootView = content
                return cell
            }
            let cell = SiteOrderCell(content: content)
            cell.identifier = identifier
            return cell
        }

        func openSite(at row: Int) {
            guard sites.indices.contains(row) else { return }
            model.screen = .site(sites[row].id)
        }

        @objc func openClickedSite(_ table: NSTableView) {
            openSite(at: table.clickedRow)
        }

        func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int) -> NSPasteboardWriting? {
            guard sites.indices.contains(row) else { return nil }
            let item = NSPasteboardItem()
            item.setString(sites[row].id, forType: Self.pasteboardType)
            return item
        }

        func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession,
                       willBeginAt screenPoint: NSPoint, forRowIndexes rowIndexes: IndexSet) {
            guard let row = rowIndexes.first, sites.indices.contains(row) else { return }
            draggingID = sites[row].id
            session.animatesToStartingPositionsOnCancelOrFail = true
        }

        func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession,
                       endedAt screenPoint: NSPoint, operation: NSDragOperation) {
            draggingID = nil
            refresh(tableView, restoreSelection: true)
        }

        private func draggedSite(_ info: NSDraggingInfo, in table: NSTableView) -> String? {
            guard let source = info.draggingSource as? NSTableView, source === table,
                  let id = info.draggingPasteboard.string(forType: Self.pasteboardType),
                  id == draggingID, sites.map(\.id) == model.sites.map(\.id) else { return nil }
            return id
        }

        func tableView(_ tableView: NSTableView, validateDrop info: NSDraggingInfo,
                       proposedRow row: Int, proposedDropOperation dropOperation: NSTableView.DropOperation) -> NSDragOperation {
            guard (0...sites.count).contains(row), draggedSite(info, in: tableView) != nil else { return [] }
            info.animatesToDestination = !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
            tableView.setDropRow(row, dropOperation: .above)
            return .move
        }

        func tableView(_ tableView: NSTableView, acceptDrop info: NSDraggingInfo,
                       row: Int, dropOperation: NSTableView.DropOperation) -> Bool {
            guard let id = draggedSite(info, in: tableView), dropOperation == .above else { return false }
            let moved = moveSite(id, beforeRow: row, in: tableView)
            if moved, let destination = sites.firstIndex(where: { $0.id == id }) {
                let destinationFrame = tableView.frameOfCell(atColumn: 0, row: destination)
                info.enumerateDraggingItems(options: [], for: tableView, classes: [NSPasteboardItem.self], searchOptions: [:]) { item, _, _ in
                    item.draggingFrame = destinationFrame
                }
            }
            draggingID = nil
            refresh(tableView, restoreSelection: true)
            return moved
        }

        @discardableResult func moveSite(_ id: String, beforeRow row: Int, in tableView: NSTableView) -> Bool {
            guard sites.map(\.id) == model.sites.map(\.id),
                  let source = sites.firstIndex(where: { $0.id == id }),
                  model.moveSite(id, beforeRow: row),
                  let destination = model.sites.firstIndex(where: { $0.id == id }) else { return false }
            let cachedSite = sites.remove(at: source)
            sites.insert(cachedSite, at: destination)
            // Retain row views as the gap closes instead of rebuilding the table.
            NSAnimationContext.runAnimationGroup { context in
                context.duration = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 0 : 0.18
                context.allowsImplicitAnimation = true
                tableView.moveRow(at: source, to: destination)
            }
            return true
        }

        func menu(for row: Int) -> NSMenu? {
            guard sites.indices.contains(row) else { return nil }
            let siteID = sites[row].id
            let publishing = model.publishing.contains(siteID)
            let menu = NSMenu()
            menu.autoenablesItems = false
            for (title, action, enabled) in [
                ("Website", #selector(openWebsite(_:)), true),
                ("Settings", #selector(openSettings(_:)), true),
                (publishing ? "Publishing…" : "Publish", #selector(publishSite(_:)), !publishing)
            ] {
                let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
                item.target = self
                item.representedObject = siteID
                item.isEnabled = enabled
                menu.addItem(item)
            }
            return menu
        }

        @objc private func openWebsite(_ item: NSMenuItem) {
            guard let id = item.representedObject as? String else { return }
            Task {
                if let address = try? await API.shared.siteURL(id), let url = URL(string: address) {
                    NSWorkspace.shared.open(url)
                }
            }
        }
        @objc private func openSettings(_ item: NSMenuItem) {
            if let id = item.representedObject as? String { model.screen = .settings(id) }
        }
        @objc private func publishSite(_ item: NSMenuItem) {
            if let id = item.representedObject as? String { model.publish(id) }
        }
    }
}

private final class SiteOrderTableView: NSTableView {
    var menuForRow: ((Int) -> NSMenu?)?
    var openKeyboardSelection: ((Int) -> Void)?
    override func menu(for event: NSEvent) -> NSMenu? {
        menuForRow?(row(at: convert(event.locationInWindow, from: nil)))
    }
    override func keyDown(with event: NSEvent) {
        super.keyDown(with: event)
        if [36, 49, 76, 125, 126].contains(event.keyCode) { openKeyboardSelection?(selectedRow) }
    }
}

private final class SiteOrderCell: NSTableCellView {
    let host: SiteOrderHostingView
    init(content: RailItemContent) {
        host = SiteOrderHostingView(rootView: content)
        super.init(frame: .zero)
        host.translatesAutoresizingMaskIntoConstraints = false
        addSubview(host)
        NSLayoutConstraint.activate([
            host.leadingAnchor.constraint(equalTo: leadingAnchor), host.trailingAnchor.constraint(equalTo: trailingAnchor),
            host.topAnchor.constraint(equalTo: topAnchor), host.bottomAnchor.constraint(equalTo: bottomAnchor)
        ])
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    override var draggingImageComponents: [NSDraggingImageComponent] {
        guard let bitmap = bitmapImageRepForCachingDisplay(in: bounds) else { return [] }
        cacheDisplay(in: bounds, to: bitmap)
        let image = NSImage(size: bounds.size)
        image.addRepresentation(bitmap)
        let component = NSDraggingImageComponent(key: .icon)
        component.contents = image
        component.frame = bounds
        return [component]
    }
}

private final class SiteOrderHostingView: NSHostingView<RailItemContent> {
    // Leave click and drag handling to NSTableView across the entire row.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}
