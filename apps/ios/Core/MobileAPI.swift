import Foundation

final class NoRedirects: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
}

public actor MobileAPI {
    public let origin: URL
    private let identity: SiteIdentity
    private let session: URLSession
    private var token: String?
    private var tokenExpiresAt: Int64 = 0
    private var configuredHost: String?

    public init(origin: URL, identity: SiteIdentity) throws {
        self.origin = try Self.validatedOrigin(origin)
        self.identity = identity
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 45
        configuration.timeoutIntervalForResource = 120
        configuration.httpCookieStorage = nil
        configuration.urlCache = nil
        session = URLSession(configuration: configuration, delegate: NoRedirects(), delegateQueue: nil)
    }

    public static func validatedOrigin(_ url: URL) throws -> URL {
        guard let result = URL(string: try SessionChallengeValidator.validatedOrigin(url)) else {
            throw MobileError.invalid("Invalid publishing service address.")
        }
        return result
    }

    public func configuration() async throws -> ServiceConfiguration {
        let result: ServiceConfiguration = try await request("GET", "/config", authenticated: false)
        guard result.version == 1, result.origin == origin.absoluteString, result.maxImages == 1 else {
            throw MobileError.invalid("This service uses an unsupported publishing protocol.")
        }
        configuredHost = result.host
        return result
    }

    public func site() async throws -> SiteStatus {
        let result: SiteStatus = try await request("GET", "/site")
        guard result.ipns == identity.ipns else { throw MobileError.invalid("The service returned a different site.") }
        return result
    }

    public func setEnabled(_ enabled: Bool) async throws -> SiteStatus {
        try await request("PUT", "/connection", json: ["enabled": enabled])
    }

    public func status(id: String) async throws -> MobileOperation {
        try validateID(id)
        return try await request("GET", "/operations/\(id)")
    }

    public func upload(_ draft: LocalDraft, image: Data, configuration: ServiceConfiguration? = nil) async throws -> MobileOperation {
        guard draft.destination?.ipns == identity.ipns, draft.destination?.origin == origin else {
            throw MobileError.invalid("Reconnect this draft's original site before publishing.")
        }
        let config: ServiceConfiguration
        if let configuration { config = configuration } else { config = try await self.configuration() }
        try Self.validateUpload(draft, image: image, configuration: config)
        let boundary = "Croptop-\(UUID().uuidString)"
        var body = Data()
        for (name, value) in [("id", draft.id), ("title", draft.title), ("caption", draft.caption)] {
            body.append(Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"\(name)\"\r\n\r\n\(value)\r\n".utf8))
        }
        body.append(Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"image\"; filename=\"image\"\r\nContent-Type: \(draft.contentType)\r\n\r\n".utf8))
        body.append(image); body.append(Data("\r\n--\(boundary)--\r\n".utf8))
        return try await request("POST", "/operations", body: body, contentType: "multipart/form-data; boundary=\(boundary)")
    }

    public static func validateUpload(_ draft: LocalDraft, image: Data, configuration config: ServiceConfiguration) throws {
        guard config.enabled else { throw MobileError.invalid("Phone posting is currently unavailable at this service.") }
        guard draft.title.utf8.count <= config.maxTitleBytes else { throw MobileError.invalid("Shorten the title before publishing.") }
        guard draft.caption.utf8.count <= config.maxCaptionBytes else { throw MobileError.invalid("Shorten the caption before publishing.") }
        guard image.count <= config.maxImageBytes else { throw MobileError.invalid("This image is larger than the service's upload limit.") }
        guard config.formats.contains(draft.contentType),
              draft.contentType.unicodeScalars.allSatisfy({ CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyz/").contains($0) }) else {
            throw MobileError.invalid("This service cannot publish this image format yet. Your draft is retained.")
        }
    }

    public func prepare(id: String) async throws -> MobileOperation {
        try validateID(id)
        return try await request("POST", "/operations/\(id)/prepare", json: [:])
    }

    public func normalizedImage(id: String) async throws -> Data {
        try validateID(id)
        return try await rawRequest("GET", "/operations/\(id)/image", authenticated: true)
    }

    public func validatedProposal(_ operation: MobileOperation, draft: LocalDraft, image: Data) throws -> ValidatedProposal {
        guard let proposal = operation.proposal else { throw MobileError.invalid("The post does not have a signing proposal yet.") }
        guard let host = configuredHost, !host.isEmpty else { throw MobileError.invalid("Refresh the service configuration before signing.") }
        return try ProposalValidator.validate(ProposalValidationContext(
            expectedID: draft.id, expectedIPNS: identity.ipns, expectedTitle: draft.title,
            expectedCaption: draft.caption, expectedHost: host,
            operationID: operation.id, operationIPNS: operation.ipns, postID: operation.postID,
            title: operation.title, caption: operation.caption, mediaSHA256: operation.mediaSHA256,
            mediaType: operation.mediaType, proposalID: proposal.id, cid: proposal.cid,
            parent: proposal.parent, sequence: proposal.sequence, host: proposal.host,
            time: proposal.time, expiresAt: proposal.expiresAt, recordPayload: proposal.recordPayload,
            pushPayload: proposal.pushPayload, normalizedImage: image))
    }

    public func commit(_ operation: MobileOperation, draft: LocalDraft, image: Data) async throws -> MobileOperation {
        let payloads = try validatedProposal(operation, draft: draft, image: image)
        guard let proposal = operation.proposal else { throw MobileError.invalid("No proposal is available.") }
        return try await request("POST", "/operations/\(draft.id)/commit", json: [
            "proposalId": proposal.id,
            "recordSignature": try identity.sign(payloads.recordPayload).base64EncodedString(),
            "pushSignature": try identity.sign(payloads.pushPayload).base64EncodedString()
        ])
    }

    private func validateID(_ id: String) throws {
        guard UUID(uuidString: id)?.uuidString == id else { throw MobileError.invalid("Invalid post identifier.") }
    }

    private func authenticate() async throws {
        struct Challenge: Decodable { let id: String; let message: String; let expiresAt: Int64 }
        struct Session: Decodable { let token: String; let expiresAt: Int64 }
        let challenge: Challenge = try await request("POST", "/challenge", json: ["ipns": identity.ipns], authenticated: false)
        let payload = try SessionChallengeValidator.validate(id: challenge.id, message: challenge.message,
            expiresAt: challenge.expiresAt, origin: origin, ipns: identity.ipns)
        let response: Session = try await request("POST", "/session", json: [
            "id": challenge.id, "signature": try identity.sign(payload).base64EncodedString()
        ], authenticated: false)
        guard response.expiresAt > Int64(Date().timeIntervalSince1970), !response.token.isEmpty else {
            throw MobileError.invalid("The publishing session has expired. Try again.")
        }
        token = response.token; tokenExpiresAt = response.expiresAt
    }

    private func request<T: Decodable>(_ method: String, _ path: String, json: [String: Any]? = nil,
                                       body: Data? = nil, contentType: String = "application/json",
                                       authenticated: Bool = true) async throws -> T {
        let data = try await rawRequest(method, path, body: try json.map { try JSONSerialization.data(withJSONObject: $0) } ?? body,
                                        contentType: contentType, authenticated: authenticated)
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func rawRequest(_ method: String, _ path: String, body: Data? = nil, contentType: String = "application/json",
                            authenticated: Bool, retryAuthentication: Bool = true) async throws -> Data {
        if authenticated && (token == nil || tokenExpiresAt < Int64(Date().timeIntervalSince1970) + 30) { try await authenticate() }
        var request = URLRequest(url: origin.appendingPathComponent("v0/mobile" + path))
        request.httpMethod = method; request.httpBody = body
        request.setValue(contentType, forHTTPHeaderField: "Content-Type")
        request.setValue("no-store", forHTTPHeaderField: "Cache-Control")
        if authenticated, let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let (data, rawResponse) = try await session.data(for: request)
        guard let response = rawResponse as? HTTPURLResponse else { throw MobileError.invalid("The service did not return an HTTP response.") }
        if response.statusCode == 401 && authenticated && retryAuthentication {
            token = nil
            return try await rawRequest(method, path, body: body, contentType: contentType,
                                        authenticated: true, retryAuthentication: false)
        }
        guard (200..<300).contains(response.statusCode) else {
            struct Failure: Decodable { let error: String; let code: String? }
            let failure = try? JSONDecoder().decode(Failure.self, from: data)
            throw MobileError.service(response.statusCode, failure?.code ?? "http_error", failure?.error ?? "The publishing service could not complete this request.")
        }
        return data
    }
}
