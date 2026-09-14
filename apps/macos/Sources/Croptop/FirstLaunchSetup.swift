import Foundation

/// An interrupted setup resumes without creating another site or re-following
/// a site the user deliberately unfollowed after setup completed.
@MainActor
final class FirstLaunchSetup {
    static let welcomeSite = "croptop.eth"
    private let preferences: UserDefaults
    private let key: String
    private let sites: () async throws -> [Site]
    private let following: () async throws -> [Following]
    private let create: () async throws -> Site
    private let follow: () async throws -> Void

    init(preferences: UserDefaults, scope: String,
         sites: @escaping () async throws -> [Site], following: @escaping () async throws -> [Following],
         create: @escaping () async throws -> Site, follow: @escaping () async throws -> Void) {
        self.preferences = preferences; key = "firstLaunch:" + scope + ":"
        self.sites = sites; self.following = following; self.create = create; self.follow = follow
    }

    func prepare() async throws -> String? {
        guard !preferences.bool(forKey: key + "complete") else { return nil }
        if !preferences.bool(forKey: key + "started") {
            let existingSites = try await sites()
            let existingFollowing = try await following()
            guard existingSites.isEmpty && existingFollowing.isEmpty else {
                preferences.set(true, forKey: key + "complete")
                return nil
            }
            preferences.set(true, forKey: key + "started")
        }
        if let id = preferences.string(forKey: key + "site") { return id }
        // A successful create may outlive a failed response or an app restart.
        let existing = try await sites()
        let site: Site
        if let first = existing.first { site = first } else { site = try await create() }
        preferences.set(site.id, forKey: key + "site")
        return site.id
    }

    func finishFollowing() async throws {
        guard preferences.bool(forKey: key + "started"),
              !preferences.bool(forKey: key + "complete"),
              preferences.string(forKey: key + "site") != nil else { return }
        let list = try await following()
        if !list.contains(where: { $0.name.lowercased() == Self.welcomeSite }) { try await follow() }
        preferences.set(true, forKey: key + "complete")
    }
}
