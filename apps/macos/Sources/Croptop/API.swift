// The local console API (docs/superpowers/specs/2026-09-10-native-apps-design.md, "API contract").
import Foundation

struct Site: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var about: String?
    var ipns: String
    var domain: String?
    var lastPublished: Double?
    var archived: Bool?
    var publishedElsewhere: Bool?
    var tags: [String: String]?

    var secondLine: String {
        if publishedElsewhere == true { return "published elsewhere" }
        if let d = domain, !d.isEmpty { return d }
        return String(ipns.prefix(12)) + "…"
    }
}

struct Post: Codable, Identifiable, Hashable {
    var id: String
    var title: String
    var content: String
    var created: Double
    var articleType: Int
    var link: String
    var attachments: [String]
    var tags: [String: String]?
    var heroImage: String?
    var pinned: Double?
    var isIncludedInNavigation: Bool?
    var summary: String?

    var date: Date { Date(timeIntervalSinceReferenceDate: created) }
    var tagList: [String] { (tags ?? [:]).keys.sorted() }
    var isPage: Bool { articleType == 1 }
}

struct FeedItem: Codable, Identifiable, Hashable {
    var ipns: String
    var site: String
    var id: String
    var title: String
    var summary: String
    var link: String
    var url: String
    var created: Double
    var preview: Bool
    var pinned: Bool
    var hero: String?
    var date: Date { Date(timeIntervalSinceReferenceDate: created) }
}

struct Following: Codable, Identifiable, Hashable {
    var name: String
    var ipns: String
    var cid: String?
    var title: String?
    var error: String?
    var id: String { ipns }
    var label: String { (title?.isEmpty == false ? title! : name) }
    var secondLine: String {
        if let e = error, !e.isEmpty { return "check failed" }
        return name != ipns ? name : String(ipns.prefix(12)) + "…"
    }
}

struct Status: Codable {
    struct IPFS: Codable { var running: Bool; var peers: Int }
    var version: String
    var latest: String?
    var update: Bool
    var ipfs: IPFS
}

struct QuickGroup: Codable { var id: String; var files: [String] }

struct APIError: LocalizedError {
    var message: String
    var errorDescription: String? { message }
}

final class API {
    static let shared = API()
    let base = URL(string: ProcessInfo.processInfo.environment["CROPTOP_CONSOLE"] ?? "http://127.0.0.1:8086")!
    private let session: URLSession = {
        let c = URLSessionConfiguration.default
        c.timeoutIntervalForRequest = 30
        return URLSession(configuration: c)
    }()
    private let decoder = JSONDecoder()

    func url(_ path: String) -> URL { URL(string: path, relativeTo: base)!.absoluteURL }

    func ping() async -> Bool {
        var r = URLRequest(url: url("/v0/ping"))
        r.timeoutInterval = 2
        guard let (_, resp) = try? await session.data(for: r) else { return false }
        return (resp as? HTTPURLResponse)?.statusCode == 200
    }

    // Blocking, for applicationWillTerminate.
    func quitSync() {
        var r = URLRequest(url: url("/v0/croptop/quit"))
        r.httpMethod = "POST"
        r.timeoutInterval = 3
        let done = DispatchSemaphore(value: 0)
        session.dataTask(with: r) { _, _, _ in done.signal() }.resume()
        _ = done.wait(timeout: .now() + 3)
    }

    private func send(_ method: String, _ path: String, json: Any? = nil, form: Multipart? = nil) async throws -> Data {
        var r = URLRequest(url: url(path))
        r.httpMethod = method
        if let json = json {
            r.httpBody = try JSONSerialization.data(withJSONObject: json)
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let form = form {
            r.httpBody = form.body
            r.setValue(form.contentType, forHTTPHeaderField: "Content-Type")
        }
        let (data, resp) = try await session.data(for: r)
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if code >= 300 {
            let msg = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String
            throw APIError(message: msg ?? String(data: data, encoding: .utf8) ?? "HTTP \(code)")
        }
        return data
    }

    private func get<T: Decodable>(_ path: String) async throws -> T {
        try decoder.decode(T.self, from: try await send("GET", path))
    }

    func status() async throws -> Status { try await get("/v0/croptop/status") }
    func sites() async throws -> [Site] { try await get("/v0/planets/my") }
    func following() async throws -> [Following] { try await get("/v0/croptop/following") }
    func feed(limit: Int = 60) async throws -> [FeedItem] { try await get("/v0/croptop/feed?limit=\(limit)") }
    func posts(site: String) async throws -> [Post] { try await get("/v0/planets/my/\(site)/articles") }
    func post(site: String, id: String) async throws -> Post { try await get("/v0/planets/my/\(site)/articles/\(id)") }
    func quick(_ id: String) async throws -> QuickGroup { try await get("/v0/croptop/quick/\(id)") }
    func siteURL(_ id: String) async throws -> String {
        let d: [String: String] = try await get("/v0/croptop/sites/\(id)/url")
        return d["url"] ?? ""
    }

    func createSite(name: String, about: String) async throws -> Site {
        let f = Multipart(); f.field("name", name); f.field("about", about)
        return try decoder.decode(Site.self, from: try await send("POST", "/v0/planets/my", form: f))
    }
    func follow(_ name: String) async throws { _ = try await send("POST", "/v0/croptop/following", json: ["name": name]) }
    func unfollow(_ ipns: String) async throws { _ = try await send("DELETE", "/v0/croptop/following/\(ipns)") }
    func refreshFollowing(_ ipns: String) async throws { _ = try await send("POST", "/v0/croptop/following/\(ipns)/refresh") }
    func publish(site: String) async throws -> [String: Any] {
        let d = try await send("POST", "/v0/croptop/sites/\(site)/publish", json: [:])
        return (try? JSONSerialization.jsonObject(with: d) as? [String: Any]) ?? [:]
    }
    func update() async throws { _ = try await send("POST", "/v0/croptop/update", json: [:]) }

    // Posts are multipart, the way Planet's API takes them.
    func savePost(site: String, id: String?, form: Multipart, attachmentMode: String = "append") async throws -> Post {
        let path = id == nil ? "/v0/planets/my/\(site)/articles" : "/v0/planets/my/\(site)/articles/\(id!)?attachmentMode=\(attachmentMode)"
        return try decoder.decode(Post.self, from: try await send("POST", path, form: form))
    }
    func deletePost(site: String, id: String) async throws { _ = try await send("DELETE", "/v0/planets/my/\(site)/articles/\(id)") }
    func deleteAttachment(site: String, post: String, name: String) async throws {
        _ = try await send("DELETE", "/v0/planets/my/\(site)/articles/\(post)/attachments/\(name.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? name)")
    }
    func uploadQuick(_ files: [URL]) async throws -> String {
        let f = Multipart()
        for u in files { f.file("files", u) }
        let d = try await send("POST", "/v0/croptop/quick", form: f)
        let g = try decoder.decode(QuickGroup.self, from: d)
        return g.id
    }
    func quickFile(_ id: String, _ name: String) -> URL {
        url("/v0/croptop/quick/\(id)/\(name.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? name)")
    }
    func discardQuick(_ id: String) async { _ = try? await send("DELETE", "/v0/croptop/quick/\(id)") }

    // A file of one of my sites as the console serves it (attachments, covers).
    func siteFile(_ site: String, _ path: String) -> URL {
        url("/\(site)/\(path.split(separator: "/").map { $0.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? String($0) }.joined(separator: "/"))")
    }
}

final class Multipart {
    private let boundary = "croptop-\(UUID().uuidString)"
    private(set) var body = Data()
    var contentType: String { "multipart/form-data; boundary=\(boundary)" }

    func field(_ name: String, _ value: String) {
        body.append("--\(boundary)\r\nContent-Disposition: form-data; name=\"\(name)\"\r\n\r\n\(value)\r\n".data(using: .utf8)!)
    }
    func file(_ name: String, _ url: URL) {
        guard let data = try? Data(contentsOf: url) else { return }
        file(name, filename: url.lastPathComponent, data: data)
    }
    func file(_ name: String, filename: String, data: Data) {
        body.append("--\(boundary)\r\nContent-Disposition: form-data; name=\"\(name)\"; filename=\"\(filename)\"\r\nContent-Type: application/octet-stream\r\n\r\n".data(using: .utf8)!)
        body.append(data)
        body.append("\r\n".data(using: .utf8)!)
    }
    func close() { body.append("--\(boundary)--\r\n".data(using: .utf8)!) }
}
