// The local console API (docs/superpowers/specs/2026-09-10-native-apps-design.md, "API contract").
import Foundation

struct Site: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var about: String?
    var ipns: String
    var domain: String?
    var updated: Double?
    var contributors: [Contributor]?
    var aggregation: [String]?
    var isCollaborative: Bool { !(contributors ?? []).isEmpty || !(aggregation ?? []).isEmpty }
    var lastPublished: Double?
    var archived: Bool?
    var publishedElsewhere: Bool?
    var tags: [String: String]?
    var croptopCustomDomain: String?
    var croptopGateway: String?
    var croptopHost: String?
    var croptopName: String?
    var plausibleEnabled: Bool?
    var plausibleDomain: String?
    var plausibleAPIServer: String?
    var twitterUsername: String?
    var githubUsername: String?
    var telegramUsername: String?
    var mastodonUsername: String?
    var discordLink: String?
    var socialProfiles: [String: String] { ["twitterUsername": twitterUsername ?? "", "githubUsername": githubUsername ?? "", "telegramUsername": telegramUsername ?? "", "mastodonUsername": mastodonUsername ?? "", "discordLink": discordLink ?? ""] }
    var doNotIndex: Bool?
    var customCodeHead: String?
    var customCodeBodyEnd: String?

    var secondLine: String {
        if publishedElsewhere == true { return "published elsewhere" }
        if let d = croptopCustomDomain, !d.isEmpty { return d }
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
    var heroImageWidth: Double?
    var heroImageHeight: Double?
    var videoFilename: String?
    var audioFilename: String?
    var pinned: Double?
    var isIncludedInNavigation: Bool?
    var summary: String?
    var modified: Double?
    var originalSiteName: String?
    var originalSiteDomain: String?
    var originalPostID: String?
    var submissionTargets: [String]?
    var originalURL: URL? {
        guard let source = originalSiteDomain, let post = originalPostID,
              source.range(of: "^k51[a-z0-9]{20,100}$", options: .regularExpression) != nil,
              UUID(uuidString: post) != nil else { return nil }
        return URL(string: "https://\(source).crop.top/\(post)/")
    }

    var date: Date { Date(timeIntervalSinceReferenceDate: created) }
    var tagList: [String] { (tags ?? [:]).keys.sorted() }
    var isPage: Bool { articleType == 1 }

    var tileImage: String? {
        if let heroImage, !heroImage.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return heroImage }
        if let videoFilename, !videoFilename.isEmpty { return "_videoThumbnail.png" }
        if let audioFilename, !audioFilename.isEmpty { return "_audioThumbnail.png" }
        let imageExtensions: Set<String> = ["png", "jpg", "jpeg", "gif", "webp", "avif", "heic", "heif", "tif", "tiff", "bmp"]
        return attachments.first { imageExtensions.contains(($0 as NSString).pathExtension.lowercased()) }
    }

    var hasWidgetPreview: Bool {
        WidgetPreviewSource.hasPreview(content: content, attachments: attachments)
    }

    var hasTileAspectRatio: Bool {
        guard let width = heroImageWidth, let height = heroImageHeight,
              width.isFinite, height.isFinite, width > 0, height > 0 else { return false }
        let ratio = width / height
        return ratio.isFinite && ratio > 0
    }

    var tileAspectRatio: Double {
        hasTileAspectRatio ? heroImageWidth! / heroImageHeight! : 1
    }
}

struct FeedItem: Codable, Identifiable, Hashable {
    var ipns: String
    var site: String
    var postID: String
    var title: String
    var summary: String
    var link: String
    var url: String
    var created: Double
    var preview: Bool
    var pinned: Bool
    var hero: String?
    var siteID: String?
    var content: String?
    var attachments: [String]?

    var id: String { (siteID.map { "owned:" + $0 } ?? "following:" + ipns) + "/" + postID }
    var date: Date { Date(timeIntervalSinceReferenceDate: created) }

    enum CodingKeys: String, CodingKey {
        case ipns, site, title, summary, link, url, created, preview, pinned, hero, siteID, content, attachments
        case postID = "id"
    }
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

struct TemplateSetting: Codable {
    var name: String?
    var description: String?
    var advanced: Bool?
}
struct TemplateMetadata: Codable { var settings: [String: TemplateSetting]? }
struct GatewayChoice: Codable, Identifiable {
    var Key: String
    var Name: String
    var id: String { Key }
}


struct APIError: LocalizedError {
    var message: String
    var status: Int?
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

    private func send(_ method: String, _ path: String, json: Any? = nil, form: Multipart? = nil, timeout: TimeInterval = 30) async throws -> Data {
        var r = URLRequest(url: url(path))
        r.httpMethod = method
        r.timeoutInterval = timeout
        if let json = json {
            r.httpBody = try JSONSerialization.data(withJSONObject: json)
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let form = form {
            form.close()
            r.httpBody = form.body
            r.setValue(form.contentType, forHTTPHeaderField: "Content-Type")
        }
        let (data, resp) = try await session.data(for: r)
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if code >= 300 {
            let msg = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String
            throw APIError(message: msg ?? String(data: data, encoding: .utf8) ?? "HTTP \(code)", status: code)
        }
        return data
    }

    private func get<T: Decodable>(_ path: String) async throws -> T {
        try decoder.decode(T.self, from: try await send("GET", path))
    }

    func status() async throws -> Status { try await get("/v0/croptop/status") }
    func sites() async throws -> [Site] { try await get("/v0/planets/my") }
    func following() async throws -> [Following] { try await get("/v0/croptop/following") }
    func feed(limit: Int = 60, source: String = "following", ipns: String? = nil, offset: Int = 0) async throws -> [FeedItem] {
        var request = URLComponents()
        request.path = "/v0/croptop/feed"
        request.queryItems = [URLQueryItem(name: "limit", value: String(limit)), URLQueryItem(name: "source", value: source), URLQueryItem(name: "offset", value: String(offset))]
        if let ipns { request.queryItems?.append(URLQueryItem(name: "ipns", value: ipns)) }
        return try await get(request.string!)
    }
    func posts(site: String) async throws -> [Post] { try await get("/v0/planets/my/\(site)/articles") }
    func post(site: String, id: String) async throws -> Post { try await get("/v0/planets/my/\(site)/articles/\(id)") }
    func collaboration(_ id: String) async throws -> CollaborationState {
        return try await get("/v0/croptop/sites/\(id)/collaboration")
    }
    func mergeSources(_ id: String, names: [String]? = nil) async throws -> CollaborationState {
        let suffix = names == nil ? "/refresh" : "/sources"
        let body: [String: Any] = names.map { ["names": $0] } ?? [:]
        return try decoder.decode(CollaborationState.self, from: try await send("POST", "/v0/croptop/sites/\(id)/collaboration" + suffix, json: body, timeout: 600))
    }
    func siteURL(_ id: String) async throws -> String {
        let d: [String: String] = try await get("/v0/croptop/sites/\(id)/url")
        return d["url"] ?? ""
    }

    func createSite(name: String, about: String, avatar: URL? = nil, sources: String? = nil) async throws -> Site {
        let f = Multipart(); f.field("name", name); f.field("about", about)
        if let sources { f.field("sources", sources) }
        if let avatar { f.file("avatar", filename: avatar.lastPathComponent, data: try Data(contentsOf: avatar)) }
        return try decoder.decode(Site.self, from: try await send("POST", "/v0/planets/my", form: f))
    }
    func follow(_ name: String) async throws { _ = try await send("POST", "/v0/croptop/following", json: ["name": name], timeout: 900) }
    func unfollow(_ ipns: String) async throws { _ = try await send("DELETE", "/v0/croptop/following/\(ipns)") }
    func refreshFollowing(_ ipns: String) async throws { _ = try await send("POST", "/v0/croptop/following/\(ipns)/refresh") }
    func publish(site: String) async throws -> [String: Any] {
        let d = try await send("POST", "/v0/croptop/sites/\(site)/publish", json: [:], timeout: 600)
        return (try? JSONSerialization.jsonObject(with: d) as? [String: Any]) ?? [:]
    }
    func update() async throws { _ = try await send("POST", "/v0/croptop/update", json: [:]) }

    func site(_ id: String) async throws -> Site { try await get("/v0/planets/my/\(id)") }
    func templateMetadata() async throws -> TemplateMetadata { try await get("/v0/croptop/template") }
    func gateways() async throws -> [GatewayChoice] { try await get("/v0/croptop/gateways") }
    func templateSettings(_ id: String) async throws -> [String: Any] {
        let data = try await send("GET", "/v0/croptop/sites/\(id)/settings")
        guard let values = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw APIError(message: "Could not read site settings.") }
        return values
    }
    func saveSite(_ id: String, values: [String: Any]) async throws {
        _ = try await send("PUT", "/v0/croptop/sites/\(id)", json: values)
    }
    func saveTemplateSettings(_ id: String, values: [String: Any]) async throws {
        _ = try await send("PATCH", "/v0/croptop/sites/\(id)/settings", json: values)
    }
    func openShop(_ id: String) async throws -> URL {
        var request = URLRequest(url: url("/v0/croptop/sites/\(id)/shop"))
        request.httpMethod = "POST"
        request.setValue("1", forHTTPHeaderField: "X-Croptop-Shop")
        let (data, response) = try await session.data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200,
              let result = try JSONSerialization.jsonObject(with: data) as? [String: String],
              let path = result["url"], path.hasPrefix("/shop#") else {
            throw APIError(message: "Could not open shop creation. Check the connected node is up to date.")
        }
        return url(path)
    }
    func openShopConnection(_ id: String, targets: [String: String], category: String) async throws -> URL {
        var request = URLRequest(url: url("/v0/croptop/sites/\(id)/shop-setup"))
        request.httpMethod = "POST"
        request.setValue("1", forHTTPHeaderField: "X-Croptop-Shop")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: ["targets": targets, "category": category])
        let (data, response) = try await session.data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200,
              let result = try JSONSerialization.jsonObject(with: data) as? [String: String],
              let path = result["url"], (path.hasPrefix("/shop-setup#") || path.hasPrefix("/shop-connect#")) else {
            let result = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
            throw APIError(message: result?["error"] as? String ?? "Could not open shop setup. Check the connected node is up to date.")
        }
        return url(path)
    }
    func saveAvatar(_ id: String, file: URL) async throws {
        let f = Multipart(); f.file("avatar", filename: file.lastPathComponent, data: try Data(contentsOf: file))
        _ = try await send("POST", "/v0/planets/my/\(id)", form: f)
    }
    func claimName(_ id: String, name: String) async throws -> Site {
        try decoder.decode(Site.self, from: try await send("POST", "/v0/croptop/sites/\(id)/name", json: ["name": name]))
    }
    func exportKey(_ id: String) async throws -> Data { try await send("GET", "/v0/croptop/sites/\(id)/key") }
    func deleteSite(_ id: String) async throws { _ = try await send("DELETE", "/v0/planets/my/\(id)") }
    func renderMarkdown(_ content: String) async throws -> String {
        var request = URLRequest(url: url("/v0/croptop/markdown"))
        request.httpMethod = "POST"
        request.httpBody = Data(content.utf8)
        request.setValue("text/plain; charset=utf-8", forHTTPHeaderField: "Content-Type")
        let (data, response) = try await session.data(for: request)
        guard let response = response as? HTTPURLResponse, response.statusCode == 200 else { throw APIError(message: "Preview could not load. Try again.") }
        return String(decoding: data, as: UTF8.self)
    }

    // Posts are multipart, the way Planet's API takes them.
    func savePost(site: String, id: String?, form: Multipart, attachmentMode: String = "append") async throws -> Post {
        let path = id == nil ? "/v0/planets/my/\(site)/articles" : "/v0/planets/my/\(site)/articles/\(id!)?attachmentMode=\(attachmentMode)"
        return try decoder.decode(Post.self, from: try await send("POST", path, form: form))
    }
    func deletePost(site: String, id: String) async throws { _ = try await send("DELETE", "/v0/planets/my/\(site)/articles/\(id)") }
    func deleteAttachment(site: String, post: String, name: String) async throws {
        _ = try await send("DELETE", "/v0/planets/my/\(site)/articles/\(post)/attachments/\(name.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? name)")
    }

    // A file of one of my sites as the console serves it (attachments, covers).
    func siteFile(_ site: String, _ path: String) -> URL {
        url("/\(site)/\(path.split(separator: "/").map { $0.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? String($0) }.joined(separator: "/"))")
    }
}

final class Multipart {
    private let boundary = "croptop-\(UUID().uuidString)"
    private(set) var body = Data()
    private var closed = false
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
    func close() {
        guard !closed else { return }
        closed = true
        body.append("--\(boundary)--\r\n".data(using: .utf8)!)
    }
}

struct Contributor: Codable, Hashable, Identifiable {
    var ipns: String
    var name: String
    var mode: String
    var id: String { ipns }
    var label: String { name.isEmpty || name == ipns ? String(ipns.prefix(14)) + "…" : name }
}
struct CollaborationState: Codable {
    var contributors: [Contributor]
    var errors: [String: String]
    var updated: Double?
}
