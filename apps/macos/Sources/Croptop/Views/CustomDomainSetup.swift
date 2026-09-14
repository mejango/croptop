import SwiftUI
import AppKit

struct CustomDomainConfiguration {
    let hostname: String
    let ipns: String

    init?(domain: String, ipns: String) {
        guard let hostname = Self.normalizedHostname(domain),
              ipns.range(of: #"\A(?:k51[a-z0-9]{59}|Qm[1-9A-HJ-NP-Za-km-z]{44})\z"#, options: .regularExpression) != nil else { return nil }
        self.hostname = hostname
        self.ipns = ipns
    }

    /// Keep this hostname boundary in step with internal/gateway.normalizeCustomDomain.
    static func normalizedHostname(_ raw: String) -> String? {
        let hostname = raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !hostname.isEmpty, hostname.utf8.count <= 253 else { return nil }
        let labels = hostname.split(separator: ".", omittingEmptySubsequences: false)
        guard labels.count >= 2 else { return nil }
        for label in labels {
            guard !label.isEmpty, label.utf8.count <= 63,
                  label.first != "-", label.last != "-",
                  label.utf8.allSatisfy({ (97...122).contains($0) || (48...57).contains($0) || $0 == 45 }) else { return nil }
        }
        let tld = String(labels[labels.count - 1])
        guard tld.count >= 2, tld.hasPrefix("xn--") || tld.utf8.allSatisfy({ (97...122).contains($0) }),
              !["eth", "sol", "bit", "localhost", "local", "internal", "onion", "invalid"].contains(tld),
              hostname != "home.arpa", !hostname.hasSuffix(".home.arpa") else { return nil }
        return hostname
    }

    var publishingHost: String { "https://" + hostname }
    var dnsLink: String { "dnslink=/ipns/" + ipns }
    var serverCommand: String {
        "croptop host --domain \(hostname) \\\n  --root \(ipns) \\\n  --listen 127.0.0.1:8090 --data /var/lib/croptop"
    }
    var caddyfile: String {
        "\(hostname) {\n    reverse_proxy 127.0.0.1:8090\n}"
    }

    var setupInstructions: String {
        """
        Connect \(hostname) to Croptop

        1. Hosting
        Install the Croptop CLI and Caddy on a server with a public IP address. Allow incoming connections on ports 80 and 443, and give Croptop write access to /var/lib/croptop.

        Run Croptop as a service:
        \(serverCommand)

        Caddyfile:
        \(caddyfile)

        2. DNS and HTTPS
        Point the A record for \(hostname) to the server's public IPv4 address. In Namecheap, use @ for the root domain or the subdomain prefix (such as www) in the Host field. After DNS points to the server, start Caddy to enable HTTPS.
        A DNSLink TXT record is optional: the command above already connects this site.

        3. Publishing
        In Croptop's Domain settings, choose Publish here for \(publishingHost), then Save & publish. Future publishes automatically send the site files to this host.
        """
    }
}

struct CustomDomainSetup: View {
    @Binding var domain: String
    @Binding var host: String
    let ipns: String
    @State private var copied: String?

    private var configuration: CustomDomainConfiguration? { CustomDomainConfiguration(domain: domain, ipns: ipns) }
    private var hostname: String? { CustomDomainConfiguration.normalizedHostname(domain) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Labeled(title: "Custom domain", help: "Use a domain you own. Connect its hosting below before saving.") {
                TextField("example.com", text: $domain)
                    .field(compact: true)
                    .frame(maxWidth: 440, alignment: .leading)
                    .accessibilityLabel("Custom domain")
                if !domain.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, hostname == nil {
                    Text("Enter a DNS domain such as example.com, without https://, a path or a port. Use Punycode for international names.")
                        .font(Theme.formHelp).foregroundColor(Theme.muted)
                }
            }
            DisclosureGroup("DNS and hosting setup") {
                VStack(alignment: .leading, spacing: 24) {
                    if configuration != nil {
                        serverStep
                        dnsStep
                        publishStep
                    } else {
                        Text(hostname == nil ? "Enter your domain above to see its setup details." : "Your site address is needed to prepare the setup details.")
                            .font(Theme.formHelp).foregroundColor(Theme.muted)
                    }
                }
                .font(Theme.formText)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 8)
            }
            .font(Theme.formText)
        }
        .onChange(of: domain) { _ in copied = nil }
    }

    private var serverStep: some View {
        SetupStep(number: 1, title: "Get your hosting ready") {
            Text("Your domain needs a host running Croptop. Send these instructions to whoever manages your website, or open the server setup below.")
            if let configuration {
                copyButton("setup instructions", text: configuration.setupInstructions)
                DisclosureGroup("Server setup") {
                    VStack(alignment: .leading, spacing: 16) {
                        Text("Install the Croptop CLI and Caddy on a server with a public IP address. Allow incoming connections on ports 80 and 443, and give Croptop write access to /var/lib/croptop.")
                        codeBlock("Croptop command", text: configuration.serverCommand)
                        codeBlock("Caddyfile", text: configuration.caddyfile)
                        Text("Keep Croptop running as a service. After connecting your domain in the next step, start Caddy to enable HTTPS.")
                        Link("Caddy setup guide", destination: URL(string: "https://caddyserver.com/docs/automatic-https")!)
                            .foregroundColor(Theme.ink)
                    }.padding(.top, 8)
                }.disclosureGroupStyle(FullRowDisclosureStyle())
            }
        }
    }

    private var dnsStep: some View {
        SetupStep(number: 2, title: "Point your domain to your host") {
            Text("Ask your host for its IP address. In Namecheap, open Advanced DNS and add an A record with that address. If you use another DNS provider, add it there.")
            Text("Use @ in the Host field for your main domain, or www for a www address.")
                .font(Theme.formHelp).foregroundColor(Theme.muted)
            Link("Namecheap DNS guide", destination: URL(string: "https://www.namecheap.com/support/knowledgebase/article.aspx/579/2237/which-record-type-option-should-i-choose-for-the-information-im-about-to-enter/")!)
                .foregroundColor(Theme.ink)
            if let configuration {
                DisclosureGroup("Optional DNSLink record") {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("The server setup above already connects your site. Add this TXT record only if you also want other IPFS services to find it through your domain.")
                        Text("Use _dnslink for your main domain, or _dnslink.www for a www address. For another subdomain, replace www with its prefix.")
                            .font(Theme.formHelp).foregroundColor(Theme.muted)
                        codeBlock("TXT value", text: configuration.dnsLink)
                    }.padding(.top, 8)
                }.disclosureGroupStyle(FullRowDisclosureStyle())
            }
        }
    }

    private var publishStep: some View {
        SetupStep(number: 3, title: "Start publishing here") {
            Text("When your host is ready, choose Publish here, then Save & publish. Future publishes will send your site files here automatically.")
            if let configuration {
                let selected = host.trimmingCharacters(in: .whitespacesAndNewlines) == configuration.publishingHost
                Button("Publish here") { host = configuration.publishingHost }
                .buttonStyle(TextActionButtonStyle())
                .disabled(selected)
                if selected {
                    Label("Publishing to " + configuration.publishingHost, systemImage: "checkmark")
                        .textSelection(.enabled)
                } else {
                    Text(configuration.publishingHost).foregroundColor(Theme.muted).textSelection(.enabled)
                }
            }
        }
    }

    private func codeBlock(_ title: String, text: String) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                Text(title).font(Theme.formLabel)
                Spacer(minLength: 0)
                copyButton(title, text: text)
            }
            Text(text).font(Theme.formText)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(10)
                .background(Theme.rule.opacity(0.25))
        }
    }

    private func copyButton(_ title: String, text: String) -> some View {
        Button {
            NSPasteboard.general.clearContents()
            if NSPasteboard.general.setString(text, forType: .string) { copied = title }
        } label: {
            Label(copied == title ? "Copied" : "Copy " + title, systemImage: copied == title ? "checkmark" : "doc.on.doc")
        }
        .buttonStyle(TextActionButtonStyle())
        .accessibilityLabel(copied == title ? title + " copied" : "Copy " + title)
    }
}
