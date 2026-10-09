import Foundation
import Darwin

/// Reads only regular files, enforcing the limit on bytes actually read as well
/// as the initial size. A provider can change its file after that size check.
public enum BoundedFile {
    public static func read(url: URL, maxBytes: Int) throws -> Data {
        guard url.isFileURL, maxBytes >= 0, maxBytes < Int.max else {
            throw MobileError.invalid("The selected file cannot be read safely.")
        }
        // O_NONBLOCK also prevents a replaced file from blocking on a FIFO.
        let descriptor = url.withUnsafeFileSystemRepresentation { path -> Int32 in
            guard let path else { return -1 }
            return Darwin.open(path, O_RDONLY | O_NONBLOCK | O_CLOEXEC)
        }
        guard descriptor >= 0 else { throw MobileError.storage("The selected file could not be opened.") }
        let handle = FileHandle(fileDescriptor: descriptor, closeOnDealloc: true)
        defer { try? handle.close() }

        var information = stat()
        guard fstat(descriptor, &information) == 0,
              information.st_mode & S_IFMT == S_IFREG,
              information.st_size >= 0 else {
            throw MobileError.invalid("Choose an image file, not a folder or another type of attachment.")
        }
        guard information.st_size <= Int64(maxBytes) else { throw tooLarge }

        var result = Data()
        while true {
            // One extra byte distinguishes an exact-limit file from overflow,
            // without ever loading the rest of an unexpectedly large file.
            let requested = min(64 * 1024, maxBytes - result.count + 1)
            guard let chunk = try handle.read(upToCount: requested), !chunk.isEmpty else { return result }
            result.append(chunk)
            guard result.count <= maxBytes else { throw tooLarge }
        }
    }

    private static var tooLarge: MobileError {
        .invalid("This image is too large to save. Choose a smaller image.")
    }
}
