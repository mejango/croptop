import Foundation

// Public releases use three numeric components. Unknown/dev bundle versions
// cannot establish that an app update is needed. Match the engine's comparison
// of release cores, ignoring tag prefixes and build/prerelease suffixes.
enum AppVersion {
    static func newerRelease(installed: String?, latest: String?) -> String? {
        guard let installed, let latest,
              let current = components(installed), let candidate = components(latest)
        else { return nil }
        return current.lexicographicallyPrecedes(candidate) ? latest : nil
    }

    private static func components(_ version: String) -> [Int]? {
        var value = version.trimmingCharacters(in: .whitespacesAndNewlines)
        if value.hasPrefix("v") { value.removeFirst() }
        let core = value.prefix { $0 != "-" && $0 != "+" }
        let parts = core.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 3, parts.allSatisfy({ !$0.isEmpty && $0.allSatisfy({ $0 >= "0" && $0 <= "9" }) }) else { return nil }
        let numbers = parts.compactMap { Int($0) }
        return numbers.count == 3 ? numbers : nil
    }
}
