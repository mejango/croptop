import XCTest
@testable import Croptop

final class CustomDomainSetupTests: XCTestCase {
    private let ipns = "k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb"

    func testHostnameNormalizationPreservesSubdomainsWithoutGuessingRegistration() {
        XCTAssertEqual(CustomDomainConfiguration.normalizedHostname(" WWW.Example.Co.UK\n"), "www.example.co.uk")
        XCTAssertEqual(CustomDomainConfiguration.normalizedHostname("xn--bcher-kva.de"), "xn--bcher-kva.de")
        XCTAssertEqual(CustomDomainConfiguration.normalizedHostname(String(repeating: "a", count: 63) + ".com"), String(repeating: "a", count: 63) + ".com")
    }

    func testOnlyConventionalDNSHostnamesCanGenerateConfiguration() {
        let invalid = [
            "", "localhost", ".example.com", "example.com.", "example..com", "example.com..", "-example.com", "example-.com",
            "https://example.com", "user@example.com", "example.com:443", "example.com/path", "example.com?x", "example.com#x",
            "127.0.0.1", "example.c", "example.c1", "example.co-m", "jango.eth", "jango.sol", "jango.bit", "server.local", "server.internal", "site.onion",
            "home.arpa", "server.home.arpa", "bücher.de", "example.com;id", "example.com$(id)", "example.com`id`",
            "example.com'", "example.com\"", "example.com\\", "example.\ncom", "example.\tcom",
            String(repeating: "a", count: 64) + ".com", Array(repeating: String(repeating: "a", count: 63), count: 4).joined(separator: ".")
        ]
        for domain in invalid {
            XCTAssertNil(CustomDomainConfiguration.normalizedHostname(domain), domain)
            XCTAssertNil(CustomDomainConfiguration(domain: domain, ipns: ipns), domain)
        }
    }

    func testConfigurationUsesValidatedValuesOnly() throws {
        let config = try XCTUnwrap(CustomDomainConfiguration(domain: " WWW.Example.COM ", ipns: ipns))
        XCTAssertEqual(config.hostname, "www.example.com")
        XCTAssertEqual(config.publishingHost, "https://www.example.com")
        XCTAssertEqual(config.dnsLink, "dnslink=/ipns/" + ipns)
        XCTAssertTrue(config.serverCommand.contains("--domain www.example.com"))
        XCTAssertTrue(config.serverCommand.contains("--root " + ipns))
        XCTAssertTrue(config.caddyfile.hasPrefix("www.example.com {\n"))
    }

    func testInvalidIPNSCannotReachCopiedShellOrDNSValues() {
        for invalid in ["", "k51short", ipns.uppercased(), ipns + "\n", ipns + " ", ipns + ";id", ipns + "$(id)", ipns + "/path"] {
            XCTAssertNil(CustomDomainConfiguration(domain: "example.com", ipns: invalid), invalid)
        }
        XCTAssertNotNil(CustomDomainConfiguration(domain: "example.com", ipns: ipns))
    }
}
