import XCTest
@testable import CroptopMobileCore

final class BoundedFileTests: XCTestCase {
    private var directory: URL!

    override func setUpWithError() throws {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("croptop-bounded-test-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws { try FileManager.default.removeItem(at: directory) }

    func testReturnsBytesBelowAndAtLimit() throws {
        let bytes = Data([1, 2, 3, 4])
        let file = try fixture(bytes)
        XCTAssertEqual(try BoundedFile.read(url: file, maxBytes: 5), bytes)
        XCTAssertEqual(try BoundedFile.read(url: file, maxBytes: 4), bytes)
    }

    func testRejectsOneByteOverLimit() throws {
        let file = try fixture(Data([1, 2, 3, 4, 5]))
        XCTAssertThrowsError(try BoundedFile.read(url: file, maxBytes: 4))
    }

    func testEmptyFileWithZeroLimitAndNonemptyFileWithZeroLimit() throws {
        XCTAssertEqual(try BoundedFile.read(url: fixture(Data()), maxBytes: 0), Data())
        XCTAssertThrowsError(try BoundedFile.read(url: fixture(Data([1])), maxBytes: 0))
    }

    func testRejectsNonregularFilesAndInvalidLimits() throws {
        XCTAssertThrowsError(try BoundedFile.read(url: directory, maxBytes: 4))
        let file = try fixture(Data([1]))
        XCTAssertThrowsError(try BoundedFile.read(url: file, maxBytes: -1))
        XCTAssertThrowsError(try BoundedFile.read(url: file, maxBytes: Int.max))
        XCTAssertThrowsError(try BoundedFile.read(url: URL(string: "https://example.com/image.png")!, maxBytes: 4))
        XCTAssertThrowsError(try BoundedFile.read(url: directory.appendingPathComponent("missing.png"), maxBytes: 4))
    }

    func testReadsAcrossChunkBoundaryAndRejectsOverflow() throws {
        let bytes = Data(repeating: 7, count: 65_537)
        let file = try fixture(bytes)
        XCTAssertEqual(try BoundedFile.read(url: file, maxBytes: bytes.count), bytes)
        XCTAssertThrowsError(try BoundedFile.read(url: file, maxBytes: bytes.count - 1))
    }

    private func fixture(_ data: Data) throws -> URL {
        let file = directory.appendingPathComponent(UUID().uuidString)
        try data.write(to: file)
        return file
    }
}
