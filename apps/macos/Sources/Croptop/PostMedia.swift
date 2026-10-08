import Foundation

func heroImageNames(_ attachments: [String]) -> [String] {
    attachments.filter { mediaKind($0) == "image" }
}

func resolvedHeroImage(_ selected: String, attachments: [String]) -> String {
    // Generated covers and thumbnails can be selected explicitly, but must not
    // displace the first user image as the default. Their names are reserved.
    selected.isEmpty ? (heroImageNames(attachments).first { !$0.hasPrefix("_") } ?? "") : selected
}

func mediaKind(_ name: String) -> String {
    switch (name as NSString).pathExtension.lowercased() {
    case "png", "jpg", "jpeg", "gif", "webp", "avif", "heic": return "image"
    case "mp4", "mov", "webm", "m4v": return "video"
    case "mp3", "m4a", "wav", "ogg", "aac", "flac": return "audio"
    default: return "file"
    }
}
