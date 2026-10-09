/// Native sheet transitions keep setup separate from a delayed image result.
/// SwiftUI calls didDismiss for the old composer before presenting setup.
public struct MobilePresentation {
    public enum Sheet: String, Identifiable { case connection, composer; public var id: String { rawValue } }
    public var sheet: Sheet?
    private var nextSheet: Sheet?
    private var returnToDraft = false

    public init() {}
    public var mayPresentCapturedDraft: Bool { sheet != .connection && nextSheet != .connection }

    public mutating func openPairing(hasSelectedDraft: Bool) {
        if sheet == .composer {
            returnToDraft = hasSelectedDraft
            nextSheet = .connection
            sheet = nil
        } else { sheet = .connection }
    }

    public mutating func didDismiss() {
        if let nextSheet { self.nextSheet = nil; sheet = nextSheet }
        else if returnToDraft { returnToDraft = false; sheet = .composer }
    }
}
