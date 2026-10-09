import Foundation

// Finish legacy login-service migration before exposing any editable screens.
@MainActor
enum EngineStartup {
    static func needsLaunch(externalConsole: Bool, bundled: Bool, legacyService: Bool,
                            retire: () async throws -> Void,
                            ping: () async -> Bool,
                            pause: () async throws -> Void) async throws -> Bool {
        if externalConsole { return false }
        if bundled && legacyService {
            try await retire()
            for _ in 0..<40 {
                if !(await ping()) { return true }
                try await pause()
            }
            throw APIError(message: "Croptop is still closing its previous version. Quit any other Croptop windows, then reopen the app.")
        }
        return !(await ping())
    }
}
