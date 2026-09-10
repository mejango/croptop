// swift-tools-version:5.9
// Croptop for macOS: a native SwiftUI client of the local console API.
// Build: swift build -c release --arch arm64 --arch x86_64 (see installer/macos.sh)
import PackageDescription

let package = Package(
    name: "Croptop",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "Croptop",
            path: "Sources/Croptop",
            swiftSettings: [.unsafeFlags(["-swift-version", "5"])]
        )
    ]
)
