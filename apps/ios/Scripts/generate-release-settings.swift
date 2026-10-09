import Foundation

struct Service: Decodable { let origin: String }
let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
let service = try JSONDecoder().decode(Service.self, from: Data(contentsOf: root.appendingPathComponent("Core/Resources/Service.json")))
guard let url = URL(string: service.origin), url.scheme == "https", let host = url.host,
      service.origin == "https://" + host else { fatalError("Expected a canonical production HTTPS origin") }
let directory = root.appendingPathComponent("Config", isDirectory: true)
let output = directory.appendingPathComponent("Service.generated.xcconfig")
let expected = Data("// Generated from Core/Resources/Service.json. Run xcodegen generate.\nCROPTOP_SERVICE_HOST = \(host)\n".utf8)
if CommandLine.arguments.contains("--check") {
    guard (try? Data(contentsOf: output)) == expected else {
        fatalError("Service settings are stale; run xcodegen generate")
    }
    print("Associated-domain settings match the packaged service origin.")
} else {
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    try expected.write(to: output, options: .atomic)
}
