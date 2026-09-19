import SwiftUI
import AppKit
import UniformTypeIdentifiers

struct SiteLogoPicker: View {
    @Binding var selection: URL?
    var current: URL?
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 16) {
                Group {
                    if let url = selection ?? current { AttachmentImage(url: url) }
                    else { Text("✂️").font(.system(size: 30)) }
                }
                .frame(width: 88, height: 88)
                .overlay(Rectangle().stroke(Theme.rule, lineWidth: Theme.border))
                VStack(alignment: .leading, spacing: 8) {
                    Button("Choose image…", action: choose).buttonStyle(BorderedButton())
                    if selection != nil {
                        Button("Remove selection") { selection = nil; error = nil }.buttonStyle(.hover).font(Theme.body(13))
                    }
                    Text("PNG, JPEG or GIF. Optional.").font(Theme.body(13)).foregroundColor(Theme.muted)
                }
            }
            if let error { Text(error).font(Theme.body(13)).foregroundColor(Theme.ink) }
        }
    }
    private func choose() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.png, .jpeg, .gif]
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        if panel.runModal() == .OK, let url = panel.url {
            guard let size = try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize, size <= 5 * 1024 * 1024, NSImage(contentsOf: url) != nil else {
                error = "Choose an image smaller than 5 MB."; return
            }
            selection = url; error = nil
        }
    }
}

struct SiteSettingsView: View {
    @EnvironmentObject var model: AppModel
    @Environment(\.colorScheme) private var colorScheme
    var siteID: String
    @State private var section = "Site"
    @State private var original: Site?
    @State private var name = ""
    @State private var about = ""
    @State private var domain = ""
    @State private var customDomain = ""
    @State private var gateway = "crop.top"
    @State private var host = "https://crop.top"
    @State private var freeName = ""
    @State private var headCode = ""
    @State private var bodyCode = ""
    @State private var privateSearch = false
    @State private var analyticsDomain = ""
    @State private var analyticsServer = "plausible.io"
    @State private var avatar: URL?
    @State private var values: [String: Any] = [:]
    @State private var originalValues: [String: Any] = [:]
    @State private var schema: [String: TemplateSetting] = [:]
    @State private var gateways: [GatewayChoice] = []
    @State private var shopTestnets = false
    @State private var openingShop = false
    @State private var connectionTestnets = false
    @State private var busy = false
    @State private var claiming = false
    @State private var claimNotice: String?
    @State private var claimError: String?
    @State private var ensNotice: String?
    @State private var error: String?
    @State private var notice: String?
    @State private var confirmDelete = false

    init(siteID: String, section: String = "Site") {
        self.siteID = siteID
        _section = State(initialValue: section)
    }

    private let sections = ["Site", "Domain", "Money", "Contributors", "Advanced"]
    private var knownENS: String? { model.following.first { $0.ipns == original?.ipns && $0.name.hasSuffix(".eth") }?.name }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            ViewThatFits(in: .horizontal) {
                HStack(alignment: .top, spacing: 16) {
                    settingsTitle.fixedSize(horizontal: true, vertical: false)
                    Spacer(minLength: 16)
                    if original != nil { settingsActions }
                }
                VStack(alignment: .leading, spacing: 12) {
                    if original != nil { settingsActions.frame(maxWidth: .infinity, alignment: .trailing) }
                    settingsTitle
                }
            }.padding(.bottom, 12)
            ScrollView(.horizontal, showsIndicators: false) { HStack(spacing: 24) {
                ForEach(sections, id: \.self) { item in
                    Button { if section != item { notice = nil }; section = item } label: {
                        Text(item).font(Theme.tab).foregroundColor(section == item ? Theme.ink : Theme.muted)
                            .padding(.vertical, 10)
                            .overlay(alignment: .bottom) { Rectangle().fill(section == item ? Theme.rule : .clear).frame(height: 1) }
                    }.buttonStyle(.hover).accessibilityAddTraits(section == item ? .isSelected : [])
                }
                Spacer(minLength: 0)
            }
            }
            if let error {
                HStack { Text(error).font(Theme.body(14)); if original == nil { Button("Retry") { Task { await load() } }.buttonStyle(BorderedButton()) } }
                    .padding(12).overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
            }
            if let notice { Text(notice).font(Theme.body(14)) }
            if original == nil {
                if error == nil { LoadingTicker(accessibilityLabel: "Loading settings") }
                Spacer()
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 24) {
                        switch section {
                        case "Domain": domainFields
                        case "Money": paymentFields
                        case "Advanced": advancedFields
                        case "Contributors": ContributorsView(siteID: siteID)
                        default: siteFields
                        }
                    }.font(Theme.formText).frame(maxWidth: 720, alignment: .leading).padding(2)
                }

            }
        }
        .padding(Theme.content)
        .disclosureGroupStyle(FullRowDisclosureStyle())
        .task(id: siteID) {
            await load()
            while !Task.isCancelled {
                do { try await Task.sleep(nanoseconds: 5_000_000_000) } catch { break }
                await refreshShopSettings()
            }
        }
        .alert("Remove this site from this computer?", isPresented: $confirmDelete) {
            Button("Remove site", role: .destructive) { Task { await removeSite() } }
            Button("Cancel", role: .cancel) {}
        } message: { Text("This removes its local posts and publishing key. Keep a backup first. Published copies may remain online.") }
    }

    private var settingsTitle: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Site settings").font(Theme.heading(28))
            if let original { Text(original.name).font(Theme.body(14)).foregroundColor(Theme.muted) }
        }
    }

    private var settingsHighlight: Color {
        guard let value = (values["highlightColor"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines),
              value.range(of: "^#?[A-Fa-f0-9]{6}$", options: .regularExpression) != nil,
              let hex = UInt32(value.replacingOccurrences(of: "#", with: ""), radix: 16) else { return Theme.hot }
        return Color(hex: hex)
    }

    private var settingsActions: some View {
        HStack(spacing: 16) {
            if section == "Contributors" {
                Button { model.publish(siteID) } label: {
                    Label("Publish", systemImage: "arrow.up.right")
                }.disabled(model.publishing.contains(siteID))
                Button { model.screen = .site(siteID) } label: {
                    Label("Done", systemImage: "checkmark")
                }
            } else {
                HStack(spacing: 8) {
                    IconActionButton("Cancel", systemImage: "xmark") { model.screen = .site(siteID) }
                        .disabled(busy)
                    IconActionButton(busy && !claiming ? "Saving…" : "Save settings", systemImage: "checkmark") { save(publish: false) }
                        .disabled(busy || name.trimmingCharacters(in: .whitespaces).isEmpty).keyboardShortcut("s")
                }
                Button { save(publish: true) } label: {
                    Text("Save & publish")
                }.buttonStyle(BorderedButton(kind: .hot))
                    .disabled(busy || name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }.buttonStyle(TextActionButtonStyle()).fixedSize(horizontal: true, vertical: false)
    }

    private var siteFields: some View {
        VStack(alignment: .leading, spacing: 24) {
            Labeled(title: "Site name") { TextField("Your site’s name", text: $name).field() }
            Labeled(title: "About", help: "A short introduction to your site.") {
                TextEditor(text: $about).font(Theme.formText).frame(height: 90).padding(8).fieldOutline()
            }
            Labeled(title: "Logo") { SiteLogoPicker(selection: $avatar, current: API.shared.siteFile(siteID, "avatar.png")) }
            appearanceFields
        }
    }

    private var domainFields: some View {
        VStack(alignment: .leading, spacing: 24) {
            VStack(alignment: .leading, spacing: 8) {
                Text("Site address").font(Theme.formLabel)
                Text("ipns://" + (original?.ipns ?? ""))
                    .font(Theme.formHelp).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if !host.isEmpty {
                Labeled(title: "Free site name") {
                    HStack(spacing: 8) {
                        TextField("yourname", text: $freeName).field().disabled(claiming)
                            .onChange(of: freeName) { _ in claimNotice = nil; claimError = nil }
                        Button(claiming ? "Claiming…" : "Claim") { Task { await claim() } }.buttonStyle(BorderedButton()).disabled(busy || freeName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }.frame(maxWidth: 440, alignment: .leading)
                    Text((URL(string: host)?.host ?? "crop.top") + "/" + (freeName.isEmpty ? "yourname" : freeName)).font(Theme.body(14)).textSelection(.enabled)
                    if claiming {
                        LoadingTicker(accessibilityLabel: "Reserving your address")
                    }
                    if let claimNotice {
                        Label(claimNotice, systemImage: "checkmark.circle.fill").font(Theme.body(14)).foregroundColor(Theme.ink)
                    }
                    if let claimError {
                        Label("Couldn’t claim this address: " + claimError, systemImage: "exclamationmark.circle").font(Theme.body(14))
                    }
                }
            }
            VStack(alignment: .leading, spacing: 8) {
                Labeled(title: "ENS name", help: "Use an ENS name you own, such as yoursite.eth. Saving the name here does not change its ENS records.") {
                    TextField("yoursite.eth", text: $domain).field().frame(maxWidth: 440, alignment: .leading)
                    if domain.isEmpty, let knownENS {
                        Button("Use " + knownENS) { domain = knownENS }.buttonStyle(BorderedButton())
                    }
                }
                DisclosureGroup("Connect your ENS name") {
                    VStack(alignment: .leading, spacing: 24) {
                        HStack(alignment: .top, spacing: 8) {
                            Text("1.").font(Theme.formLabel)
                                .frame(width: 16, alignment: .leading).accessibilityHidden(true)
                            VStack(alignment: .leading, spacing: 8) {
                                Button {
                                    let address = "ipns://" + (original?.ipns ?? "")
                                    copy(address)
                                    let ensName = domain.trimmingCharacters(in: .whitespacesAndNewlines)
                                    let url = URL(string: "https://app.ens.domains")!.appendingPathComponent(ensName)
                                    if NSWorkspace.shared.open(url) {
                                        ensNotice = "Site address copied. Finish connecting in the ENS app."
                                    } else { ensNotice = "Site address copied. Open app.ens.domains in your browser to continue." }
                                } label: {
                                    Text("Copy site address and open ENS").font(Theme.formLabel).underline()
                                }.buttonStyle(.hover)
                                    .accessibilityLabel("Step 1: Copy site address and open ENS")
                                    .disabled(domain.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || original?.ipns.isEmpty != false)
                                if domain.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                                    Text("Enter your ENS name above to continue.").font(Theme.formHelp).foregroundColor(Theme.muted)
                                }
                                if let ensNotice { Text(ensNotice).font(Theme.formText) }
                            }
                        }.fixedSize(horizontal: false, vertical: true)
                        SetupStep(number: 2, title: "Set your content hash") {
                            Text("Connect the wallet that manages your name. Choose Edit Profile → Other → Content Hash and paste the address.")
                        }
                        SetupStep(number: 3, title: "Save in your wallet") {
                            Text("Save and approve in your wallet. You only need to do this once; future publishes keep the same address.")
                        }
                    }
                    .font(Theme.formText)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 8)
                }.font(Theme.formText)
            }
            CustomDomainSetup(domain: $customDomain, host: $host, ipns: original?.ipns ?? "")
            Labeled(title: "Website gateway", help: "The service used for links to your published site.") {
                Picker("Website gateway", selection: $gateway) {
                    ForEach(gateways) { item in Text(item.Name).tag(item.Key) }
                }.labelsHidden().fixedSize()
            }
        }
    }

    private var appearanceFields: some View {
        VStack(alignment: .leading, spacing: 24) {
            if values["maintenanceMessage"] != nil {
                Labeled(title: "Announcement", help: "Shown at the top of your website. Leave blank to hide it.") {
                    TextField("A message for visitors", text: setting("maintenanceMessage")).field()
                }
            }
            if schema["backgroundColor"] != nil || schema["foregroundColor"] != nil {
                VStack(alignment: .leading, spacing: 16) {
                    Text("Website colors").font(Theme.formLabel)
                    Text("Choose colors for this site, or use automatic light and dark colors.")
                        .font(Theme.formHelp).foregroundColor(Theme.muted)
                    colorField("backgroundColor", title: "Background color")
                    colorField("foregroundColor", title: "Foreground color")
                    HStack(spacing: 12) {
                        Text("Your site").foregroundColor(websiteColor("foregroundColor"))
                        Text("Link").foregroundColor(settingsHighlight)
                    }
                    .padding(16).frame(maxWidth: 440, alignment: .leading)
                    .background(websiteColor("backgroundColor"))
                    .overlay(Rectangle().stroke(websiteColor("foregroundColor"), lineWidth: 1))
                    .accessibilityLabel("Website color preview")
                    Button("Use automatic colors") {
                        values["backgroundColor"] = ""
                        values["foregroundColor"] = ""
                    }.buttonStyle(BorderedButton())
                        .disabled(!hasCustomWebsiteColors)
                }
            }
            if values["highlightColor"] != nil {
                colorField("highlightColor", title: "Link and button color")
            }
        }
    }

    private var hasCustomWebsiteColors: Bool {
        ["backgroundColor", "foregroundColor"].contains { !((values[$0] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }

    private func websiteColor(_ key: String) -> Color {
        if key == "highlightColor" { return settingsHighlight }
        if let hex = SiteColor.hex(values[key] as? String) { return Color(hex: hex) }
        let otherKey = key == "backgroundColor" ? "foregroundColor" : "backgroundColor"
        if let other = SiteColor.hex(values[otherKey] as? String) { return Color(hex: SiteColor.contrasting(other)) }
        return Color(hex: key == "backgroundColor" ? (colorScheme == .dark ? 0x171717 : 0xFFFFFF) : (colorScheme == .dark ? 0xEEEEEE : 0x000000))
    }

    private func colorField(_ key: String, title: String) -> some View {
        Labeled(title: title) {
            HStack(spacing: 12) {
                ColorPicker(title, selection: Binding(get: {
                    websiteColor(key)
                }, set: { color in
                    if let rgb = NSColor(color).usingColorSpace(.sRGB) {
                        values[key] = String(format: "#%02X%02X%02X", Int((rgb.redComponent * 255).rounded()), Int((rgb.greenComponent * 255).rounded()), Int((rgb.blueComponent * 255).rounded()))
                    }
                }), supportsOpacity: false).labelsHidden().accessibilityLabel(title).fixedSize()
                if key != "highlightColor" {
                    TextField("Automatic", text: setting(key)).field(compact: true)
                        .frame(width: 140).accessibilityLabel(title + " hex value")
                }
            }
        }
    }

    private var paymentFields: some View {
        VStack(alignment: .leading, spacing: 24) {
            if schema["curatorAddress"] != nil || values["curatorAddress"] != nil {
                Labeled(title: "Rewards wallet", help: "The wallet that receives Croptop rewards from purchases on your site. Use an address you control. If left blank, the buyer receives those rewards.") {
                    paymentInput("curatorAddress", title: "Rewards wallet")
                        .frame(maxWidth: 440, alignment: .leading)
                }
            }
            VStack(alignment: .leading, spacing: 16) {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Shop").font(Theme.formLabel)
                    Text(shopIntroduction)
                        .font(Theme.body(13)).foregroundColor(Theme.muted)
                }
                Button(openingShop ? "Opening…" : "Create shop") {
                    Task {
                        openingShop = true
                        defer { openingShop = false }
                        do {
                            let url = try await API.shared.openShop(siteID)
                            guard NSWorkspace.shared.open(url) else { throw APIError(message: "Could not open your browser.") }
                            notice = "Continue in your browser with a wallet extension. Confirmed shops appear here automatically."
                        } catch { self.error = error.localizedDescription }
                    }
                }.buttonStyle(BorderedButton()).disabled(openingShop)
                Text("Or enter an existing shop and set up its posting permissions below.").font(Theme.formHelp).foregroundColor(Theme.muted)
                environmentMenu(testnets: $shopTestnets, label: "Shop networks")
                ForEach(collectionKeys.filter { isTestnet($0) == shopTestnets }, id: \.self) { key in
                    HStack(alignment: .top, spacing: 16) {
                        Text(settingTitle(key)).font(Theme.formLabel)
                            .frame(width: 128, alignment: .leading).padding(.top, 7)
                        VStack(alignment: .leading, spacing: 6) {
                            paymentInput(key, title: settingTitle(key))
                            if let help = shopHelp(key), !help.isEmpty {
                                Text(help).font(Theme.formHelp).foregroundColor(Theme.muted)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }.frame(maxWidth: 440, alignment: .leading)
                    }
                }
                Button("Set up posting") {
                    Task {
                        openingShop = true
                        defer { openingShop = false }
                        do {
                            let targets = Dictionary(uniqueKeysWithValues: collectionKeys
                                .filter { $0.hasSuffix("CollectionAddress") && isTestnet($0) == shopTestnets }
                                .map { ($0, setting($0).wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines)) }
                                .filter { !$0.1.isEmpty })
                            let category = setting("collectionCategory").wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines)
                            let url = try await API.shared.openShopConnection(siteID, targets: targets, category: category.isEmpty ? "1" : category)
                            guard NSWorkspace.shared.open(url) else { throw APIError(message: "Could not open your browser.") }
                            notice = "Review posting setup across your shop’s networks in your browser."
                        } catch { self.error = error.localizedDescription }
                    }
                }.buttonStyle(BorderedButton()).disabled(openingShop)
            }
            if schema["collectionCategory"] != nil || values["collectionCategory"] != nil {
                Labeled(title: "Shop category", help: shopCategoryHelp) {
                    if schema["collectionCategory"] != nil {
                        TextField("0", text: setting("collectionCategory"))
                            .field(compact: true).frame(maxWidth: 140, alignment: .leading)
                    } else {
                        Text("Automatic").font(Theme.formText)
                    }
                }
            }
            DisclosureGroup("Network connections") {
                VStack(alignment: .leading, spacing: 16) {
                    Text("The servers your website uses to read blockchain data for each network. Keep the defaults unless you want to use another provider.")
                        .font(Theme.body(13)).foregroundColor(Theme.muted)
                    environmentMenu(testnets: $connectionTestnets, label: "Network connections")
                    ForEach(networkKeys.filter { isTestnet($0) == connectionTestnets }, id: \.self) { key in settingField(key) }
                }.padding(.top, 12)
            }.font(Theme.formText)
        }.frame(maxWidth: 584, alignment: .leading)
    }

    private var shopIntroduction: String {
        "Create and connect a shop to allow your audience to buy posts."
    }

    private var shopCategoryHelp: String {
        let explanation = "A category selects the shop’s rules for adding a post: who can add it, the minimum price, and limits on the number of copies."
        if schema["collectionCategory"] != nil {
            return explanation + " Enter the category number configured by your shop."
        }
        return explanation + " This template chooses the category automatically for the buyer adding the post."
    }

    private func environmentMenu(testnets: Binding<Bool>, label: String) -> some View {
        Menu {
            Button("Production") { testnets.wrappedValue = false }
            Button("Testnets") { testnets.wrappedValue = true }
        } label: {
            (Text(testnets.wrappedValue ? "Testnets" : "Production")
                + Text("  ")
                + Text(Image(systemName: "chevron.down")).font(.system(size: 10, weight: .semibold)))
                .font(Theme.formText).foregroundColor(Theme.ink)
        }
        .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
        .accessibilityLabel(label).accessibilityValue(testnets.wrappedValue ? "Testnets" : "Production")
    }

    private func isTestnet(_ key: String) -> Bool {
        key.localizedCaseInsensitiveContains("sepolia") || key.localizedCaseInsensitiveContains("testnet")
    }

    private func shopHelp(_ key: String) -> String? {
        switch key {
        case "ethereumMainnetCollectionAddress", "ethereumSepoliaCollectionAddress": return nil
        default:
            guard let help = schema[key]?.description else { return nil }
            return help
                .replacingOccurrences(of: "(?i)\\bcollections\\b", with: "shops", options: .regularExpression)
                .replacingOccurrences(of: "(?i)\\bcollection\\b", with: "shop", options: .regularExpression)
        }
    }

    private func paymentInput(_ key: String, title: String) -> some View {
        TextField("0x…", text: setting(key)).field(compact: true).accessibilityLabel(title)
    }

    private var advancedFields: some View {
        VStack(alignment: .leading, spacing: 24) {
            VStack(alignment: .leading, spacing: 6) {
                Toggle("Ask search engines not to list this site", isOn: $privateSearch).toggleStyle(.checkbox).font(Theme.formLabel)
                Text("This is a request to search engines. It does not make the site private.").font(Theme.body(13)).foregroundColor(Theme.muted).padding(.leading, 20)
            }
            DisclosureGroup("Hosting") {
                VStack(alignment: .leading, spacing: 6) {
                    TextField("https://crop.top", text: $host).field(compact: true)
                        .frame(maxWidth: 440, alignment: .leading).disabled(claiming)
                        .accessibilityLabel("Publishing host")
                    Text("Published updates go to crop.top automatically. You can use another host here.")
                        .font(Theme.formHelp).foregroundColor(Theme.muted)
                        .fixedSize(horizontal: false, vertical: true)
                }.padding(.top, 12)
            }.font(Theme.formText)
            DisclosureGroup("Custom website code") {
                VStack(alignment: .leading, spacing: 16) {
                    Labeled(title: "Page head") { codeEditor($headCode) }
                    Labeled(title: "End of page") { codeEditor($bodyCode) }
                }.padding(.top, 12)
            }.font(Theme.formText)
            DisclosureGroup("Analytics") {
                VStack(alignment: .leading, spacing: 12) {
                    Labeled(title: "Site domain", help: "The domain you added in Plausible, such as jango.eth.sucks. Leave blank to turn analytics off.") {
                        TextField("Your site’s domain", text: $analyticsDomain).field(compact: true).frame(maxWidth: 440)
                    }
                    Labeled(title: "Analytics server", help: "Use plausible.io, or the domain of your own Plausible server.") {
                        TextField("plausible.io", text: $analyticsServer).field(compact: true).frame(maxWidth: 440)
                    }
                }
            }.font(Theme.formText)
            VStack(alignment: .leading, spacing: 16) {
                Text("Danger zone").font(Theme.formLabel).foregroundColor(.red)
                Labeled(title: "Back up your publishing key", help: "Anyone with this key can post to your site. Keep it private. You’ll need it to publish from another computer.") {
                    Button("Save key backup…") { Task { await exportKey() } }.buttonStyle(BorderedButton()).disabled(busy)
                }
                Divider()
                VStack(alignment: .leading, spacing: 10) {
                    Text("Remove this site’s local posts and publishing key from this computer.").font(Theme.formHelp).foregroundColor(Theme.muted)
                    Button { confirmDelete = true } label: { Text("Remove site…").foregroundColor(.red) }
                        .buttonStyle(BorderedButton()).disabled(busy)
                }
            }.padding(16).frame(maxWidth: 440, alignment: .leading)
                .overlay(Rectangle().strokeBorder(Color.red, lineWidth: 1))
        }
    }

    private var collectionKeys: [String] { Set(schema.keys).union(values.keys).filter { !["maintenanceMessage", "highlightColor", "backgroundColor", "foregroundColor", "curatorAddress", "collectionCategory"].contains($0) && schema[$0]?.advanced != true && !$0.hasSuffix("RPC") }.sorted() }
    private var networkKeys: [String] { Set(schema.keys).union(values.keys).filter { $0 != "collectionCategory" && (schema[$0]?.advanced == true || $0.hasSuffix("RPC")) }.sorted() }
    private func setting(_ key: String) -> Binding<String> { Binding(get: { values[key].map { String(describing: $0) } ?? "" }, set: { values[key] = $0 }) }
    private func settingTitle(_ key: String) -> String {
        schema[key]?.name ?? key.replacingOccurrences(of: "([a-z])([A-Z])", with: "$1 $2", options: .regularExpression).capitalized
    }
    private func settingField(_ key: String) -> some View {
        let title = settingTitle(key)
        return Labeled(title: title, help: schema[key]?.description) { TextField(title, text: setting(key)).field(compact: true).frame(maxWidth: 440) }
    }
    private func codeEditor(_ binding: Binding<String>) -> some View { TextEditor(text: binding).font(Theme.code).frame(height: 120).padding(8).fieldOutline() }
    private func copy(_ text: String) { NSPasteboard.general.clearContents(); NSPasteboard.general.setString(text, forType: .string); notice = "Address copied." }

    private func load() async {
        error = nil
        do {
            async let site = API.shared.site(siteID)
            async let settings = API.shared.templateSettings(siteID)
            async let metadata = API.shared.templateMetadata()
            async let choices = API.shared.gateways()
            let (s, v, m, g) = try await (site, settings, metadata, choices)
            name = s.name; about = s.about ?? ""; domain = s.domain ?? ""; gateway = s.croptopGateway ?? "crop.top"
            customDomain = s.croptopCustomDomain ?? ""
            host = s.croptopHost?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if host.isEmpty { host = "https://crop.top" }
            freeName = s.croptopName ?? ""
            headCode = s.customCodeHead ?? ""; bodyCode = s.customCodeBodyEnd ?? ""; privateSearch = s.doNotIndex ?? false
            // A previously disabled site may retain a saved domain; keep its opt-out.
            analyticsDomain = s.plausibleEnabled == true ? (s.plausibleDomain ?? "") : ""
            analyticsServer = s.plausibleAPIServer ?? "plausible.io"
            values = v; originalValues = v; schema = m.settings ?? [:]; gateways = g; original = s
            if model.posts[siteID] == nil { await model.loadPosts(siteID) }
        } catch { self.error = error.localizedDescription }
    }
    private func refreshShopSettings() async {
        guard original != nil, !busy else { return }
        do {
            let remote = try await API.shared.templateSettings(siteID)
            var changed = false
            for key in remote.keys where key.hasSuffix("CollectionAddress") || key == "collectionCategory" {
                guard let value = remote[key],
                      !NSDictionary(dictionary: [key: value]).isEqual(to: [key: originalValues[key] ?? NSNull()]) else { continue }
                if NSDictionary(dictionary: [key: values[key] ?? NSNull()]).isEqual(to: [key: originalValues[key] ?? NSNull()]) {
                    values[key] = value
                }
                originalValues[key] = value
                changed = true
            }
            if changed {
                await model.load()
                notice = "Shop settings updated. Publish when you’re ready."
            }
        } catch { /* A temporary node outage must not discard unsaved edits. */ }
    }
    private func save(publish: Bool) {
        guard let original else { return }
        busy = true; error = nil; notice = nil
        Task {
            defer { busy = false }
            do {
                for key in ["backgroundColor", "foregroundColor"] {
                    guard let value = values[key] as? String else { continue }
                    let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
                    guard trimmed.isEmpty || SiteColor.hex(trimmed) != nil else {
                        throw APIError(message: "Enter a hex color such as #FFF8ED, or leave it blank for automatic colors.")
                    }
                    values[key] = trimmed
                }
                let trimmedDomain = domain.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !trimmedDomain.contains("://") && !trimmedDomain.contains("/") else { throw APIError(message: "Enter the name only, such as yoursite.eth.") }
                host = host.trimmingCharacters(in: .whitespacesAndNewlines)
                if host.isEmpty { host = "https://crop.top" }
                if !host.isEmpty {
                    guard let u = URL(string: host), ["http", "https"].contains(u.scheme), u.host != nil else { throw APIError(message: "Enter a full host address, such as https://crop.top.") }
                }
                var changes: [String: Any] = ["name": name, "about": about, "domain": trimmedDomain, "gateway": gateway, "host": host]
                let normalizedCustomDomain: String
                if customDomain.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    normalizedCustomDomain = ""
                } else if let hostname = CustomDomainConfiguration.normalizedHostname(customDomain) {
                    normalizedCustomDomain = hostname
                } else {
                    throw APIError(message: "Enter a domain name only, such as example.com or www.example.com.")
                }
                if normalizedCustomDomain != (original.croptopCustomDomain ?? "") { changes["customDomain"] = normalizedCustomDomain }
                var custom: [String: Any] = [:]
                if headCode != (original.customCodeHead ?? "") { custom["customCodeHead"] = headCode; custom["customCodeHeadEnabled"] = !headCode.isEmpty }
                if bodyCode != (original.customCodeBodyEnd ?? "") { custom["customCodeBodyEnd"] = bodyCode; custom["customCodeBodyEndEnabled"] = !bodyCode.isEmpty }
                if privateSearch != (original.doNotIndex ?? false) { custom["doNotIndex"] = privateSearch }
                analyticsDomain = analyticsDomain.trimmingCharacters(in: .whitespacesAndNewlines)
                let analyticsEnabled = !analyticsDomain.isEmpty
                if analyticsEnabled {
                    guard let serverURL = URL(string: "https://" + analyticsServer), serverURL.host == analyticsServer, !analyticsServer.contains("/"), !analyticsServer.contains("@") else { throw APIError(message: "Enter the analytics server’s domain only, such as plausible.io.") }
                }
                if analyticsEnabled != (original.plausibleEnabled ?? false) { custom["plausibleEnabled"] = analyticsEnabled }
                if analyticsDomain != (original.plausibleDomain ?? "") { custom["plausibleDomain"] = analyticsDomain }
                if analyticsServer != (original.plausibleAPIServer ?? "plausible.io") { custom["plausibleAPIServer"] = analyticsServer }
                if !custom.isEmpty { changes["custom"] = custom }
                try await API.shared.saveSite(siteID, values: changes)
                if changes["customDomain"] != nil {
                    let saved = try await API.shared.site(siteID)
                    guard (saved.croptopCustomDomain ?? "") == normalizedCustomDomain else {
                        throw APIError(message: "The connected background service needs updating to save your custom domain. Your domain has not been saved.")
                    }
                }
                if custom.keys.contains(where: { $0.hasPrefix("plausible") }) {
                    let saved = try await API.shared.site(siteID)
                    guard (custom["plausibleEnabled"] == nil || saved.plausibleEnabled == analyticsEnabled), (custom["plausibleDomain"] == nil || saved.plausibleDomain == analyticsDomain), (custom["plausibleAPIServer"] == nil || saved.plausibleAPIServer == analyticsServer) else {
                        throw APIError(message: "The connected background service needs updating to save Plausible settings. Your analytics changes have not been saved.")
                    }
                }
                let settingsChanges = values.filter { key, value in
                    !NSDictionary(dictionary: [key: value]).isEqual(to: [key: originalValues[key] ?? NSNull()])
                }
                if !settingsChanges.isEmpty { try await API.shared.saveTemplateSettings(siteID, values: settingsChanges) }
                if let avatar { try await API.shared.saveAvatar(siteID, file: avatar) }
                await model.load(); model.screen = .site(siteID)
                if publish { model.publish(siteID) } else { model.toast("Settings saved. Publish when you’re ready.") }
            } catch { self.error = error.localizedDescription + " Changes already saved are kept; you can retry." }
        }
    }
    private func claim() async {
        guard !busy else { return }
        busy = true; claiming = true; claimError = nil; claimNotice = nil
        let requestedName = freeName.trimmingCharacters(in: .whitespacesAndNewlines)
        let requestedHost = host
        defer { busy = false; claiming = false }
        do {
            try await API.shared.saveSite(siteID, values: ["host": requestedHost])
            let site = try await API.shared.claimName(siteID, name: requestedName)
            let claimed = site.croptopName ?? requestedName
            claimNotice = "Claimed " + (URL(string: requestedHost)?.host ?? "crop.top") + "/" + claimed + ". Publish to put your site there."
            await model.load()
        } catch { claimError = error.localizedDescription }
    }
    private func exportKey() async {
        let panel = NSSavePanel(); panel.nameFieldStringValue = name.replacingOccurrences(of: "/", with: "-") + ".pem"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do { try await API.shared.exportKey(siteID).write(to: url, options: .atomic); notice = "Publishing key backup saved." }
        catch { self.error = error.localizedDescription }
    }
    private func removeSite() async {
        do { try await API.shared.deleteSite(siteID); await model.load(); model.screen = .feed }
        catch { self.error = error.localizedDescription }
    }
}
