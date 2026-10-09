// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "CroptopMobileCore",
    platforms: [.iOS(.v17), .macOS(.v13)],
    products: [.library(name: "CroptopMobileCore", targets: ["CroptopMobileCore"])],
    targets: [
        .target(name: "CroptopMobileCore", path: "Core"),
        .testTarget(name: "CroptopMobileCoreTests", dependencies: ["CroptopMobileCore"], path: "Tests")
    ]
)
