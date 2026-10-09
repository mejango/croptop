import Foundation

/// Pairing responses never enter disk caches, cookies or redirected requests.
/// Keep phone-specific security headers and error redaction in this one owner.
final class LocalPhoneConnectionClient: PhoneConnectionClient {
    private let base: URL
    private var session: URLSession!

    @MainActor init(base: URL = API.shared.base, configuration: URLSessionConfiguration = .ephemeral) {
        self.base = base
        configuration.urlCache = nil
        configuration.httpCookieStorage = nil
        configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        configuration.timeoutIntervalForRequest = 15
        configuration.timeoutIntervalForResource = 20
        session = URLSession(configuration: configuration, delegate: PhoneConnectionRedirectGuard(), delegateQueue: nil)
    }
    deinit { session.invalidateAndCancel() }

    func prepare(siteID: String, id: String, enableHosting: Bool, allowPublish: Bool) async throws -> PhonePreparation {
        try await request("POST", siteID: siteID, suffix: "/preparations", body: ["id": id, "enableHosting": enableHosting, "allowPublish": allowPublish])
    }
    func preparation(siteID: String, id: String) async throws -> PhonePreparation {
        try await request("GET", siteID: siteID, suffix: "/preparations/" + id)
    }
    func cancel(siteID: String, id: String) async throws {
        _ = try await data("DELETE", siteID: siteID, suffix: "/preparations/" + id)
    }
    func status(siteID: String, pairingID: String) async throws -> PhonePairingStatus {
        try await request("GET", siteID: siteID, suffix: "/" + pairingID)
    }
    func confirm(siteID: String, pairingID: String, code: String) async throws {
        _ = try await data("POST", siteID: siteID, suffix: "/" + pairingID + "/confirm", body: ["code": code])
    }

    private func request<T: Decodable>(_ method: String, siteID: String, suffix: String, body: [String: Any]? = nil) async throws -> T {
        do { return try JSONDecoder().decode(T.self, from: await data(method, siteID: siteID, suffix: suffix, body: body)) }
        catch let error as PhoneConnectionError { throw error }
        catch { throw PhoneConnectionError(status: 0, code: "invalid_response") }
    }

    private func data(_ method: String, siteID: String, suffix: String, body: [String: Any]? = nil) async throws -> Data {
        guard UUID(uuidString: siteID) != nil,
              base.user == nil, base.password == nil,
              base.scheme == "https" || (base.scheme == "http" && ["127.0.0.1", "localhost", "[::1]", "::1"].contains(base.host ?? "")),
              suffix.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || [45, 47, 95].contains($0) }),
              let url = URL(string: "/v0/croptop/sites/" + siteID + "/phone" + suffix, relativeTo: base) else {
            throw PhoneConnectionError(status: 0, code: "invalid_response")
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = 15
        request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        request.setValue("1", forHTTPHeaderField: "X-Croptop-Phone")
        request.setValue("no-store", forHTTPHeaderField: "Cache-Control")
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let bytes: Data
        let response: URLResponse
        do { (bytes, response) = try await session.data(for: request) }
        catch { throw PhoneConnectionError(status: 0, code: "transport") }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            // Server diagnostics can contain upstream request URLs. Only the
            // typed code/status crosses into presentation, never the raw body.
            let code = (try? JSONSerialization.jsonObject(with: bytes) as? [String: Any])?["code"] as? String
            throw PhoneConnectionError(status: status, code: code)
        }
        guard bytes.count <= 65_536 else { throw PhoneConnectionError(status: 0, code: "invalid_response") }
        return bytes
    }
}

private final class PhoneConnectionRedirectGuard: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}
