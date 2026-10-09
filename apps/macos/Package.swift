// swift-tools-version:5.9
// Croptop for macOS: a native SwiftUI client of the local console API.
// Build: swift build -c release --arch arm64 --arch x86_64 (see installer/macos.sh)
import PackageDescription

let package = Package(
    name: "Croptop",
    platforms: [.macOS(.v13)],
    dependencies: [.package(url: "https://github.com/sparkle-project/Sparkle", exact: "2.9.6")],
    targets: [
        .executableTarget(
            name: "Croptop",
            dependencies: [.product(name: "Sparkle", package: "Sparkle")],
            path: "Sources/Croptop",
            resources: [.process("Resources")],
            swiftSettings: [.unsafeFlags(["-swift-version", "5"])],
            linkerSettings: [.unsafeFlags(["-Xlinker", "-rpath", "-Xlinker", "@executable_path/../Frameworks"])]
        ),
        .testTarget(name: "CroptopTests", dependencies: ["Croptop"])
    ]
)
