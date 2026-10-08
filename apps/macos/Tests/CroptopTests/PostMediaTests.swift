import XCTest
@testable import Croptop

final class PostMediaTests: XCTestCase {
    func testDefaultFollowsAttachmentOrderAsImagesAreAddedAndRemoved() {
        var attachments = ["notes.pdf", "clip.mp4", "song.mp3", "z-first.png"]
        XCTAssertEqual(resolvedHeroImage("", attachments: attachments), "z-first.png")

        attachments.append("a-later.jpg")
        XCTAssertEqual(resolvedHeroImage("", attachments: attachments), "z-first.png")

        attachments.removeAll { $0 == "z-first.png" }
        XCTAssertEqual(resolvedHeroImage("", attachments: attachments), "a-later.jpg")

        attachments.removeAll { $0 == "a-later.jpg" }
        XCTAssertEqual(resolvedHeroImage("", attachments: attachments), "")
    }

    func testAuthorSelectionSurvivesLaterAdditionsAndUnrelatedRemovals() {
        var attachments = ["first.png", "chosen.jpg"]
        XCTAssertEqual(resolvedHeroImage("chosen.jpg", attachments: attachments), "chosen.jpg")

        attachments.append("a-new.png")
        attachments.removeAll { $0 == "first.png" }
        XCTAssertEqual(resolvedHeroImage("chosen.jpg", attachments: attachments), "chosen.jpg")
    }

    func testGeneratedImagesDoNotDisplaceTheFirstAttachedImage() {
        let attachments = ["_cover.png", "_videoThumbnail.png", "_audioThumbnail.png", "photo.png", "other.jpg"]
        XCTAssertEqual(heroImageNames(attachments), attachments, "Generated images remain available for preview and explicit selection")
        XCTAssertEqual(resolvedHeroImage("", attachments: attachments), "photo.png")
        XCTAssertEqual(resolvedHeroImage("_cover.png", attachments: attachments), "_cover.png")
    }

    func testWithoutUserImagesTheHeroRemainsAutomaticUnlessAlreadySelected() {
        for attachments in [[], ["notes.pdf", "clip.mov", "song.mp3"], ["_cover.png", "_videoThumbnail.png"]] {
            XCTAssertEqual(resolvedHeroImage("", attachments: attachments), "")
            XCTAssertEqual(resolvedHeroImage("legacy-cover.png", attachments: attachments), "legacy-cover.png")
        }
    }

    func testExistingDropImageFormatsRemainEligibleForAutomaticHero() {
        for image in ["PHOTO.AVIF", "PHOTO.HEIC", "PHOTO.JPEG", "PHOTO.PNG", "PHOTO.GIF", "PHOTO.WEBP"] {
            let attachments = ["notes.txt", image, "later.jpg"]
            XCTAssertEqual(heroImageNames(attachments), [image, "later.jpg"])
            XCTAssertEqual(resolvedHeroImage("", attachments: attachments), image)
        }
    }
}
