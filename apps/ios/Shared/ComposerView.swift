import SwiftUI
import PhotosUI
import CroptopMobileCore

struct ComposerView: View {
    @ObservedObject var model: ComposerModel
    let draft: LocalDraft
    var onSaved: (() -> Void)?
    @State private var title = ""
    @State private var caption = ""
    @State private var confirmDestination = false
    @State private var replacementImage: PhotosPickerItem?

    init(model: ComposerModel, draft: LocalDraft, onSaved: (() -> Void)? = nil) {
        self.model = model; self.draft = draft; self.onSaved = onSaved
        _title = State(initialValue: draft.title); _caption = State(initialValue: draft.caption)
    }

    var body: some View {
        Form {
            Section {
                if let image = model.previewImage {
                    Image(uiImage: image).resizable().scaledToFit().frame(maxHeight: 360)
                        .frame(maxWidth: .infinity).accessibilityLabel("Post image preview")
                } else { Label("Preview unavailable", systemImage: "photo") }
                if !draft.submitted {
                    PhotosPicker("Replace image", selection: $replacementImage, matching: .images, preferredItemEncoding: .current)
                        .disabled(model.busy)
                }
                TextField("Add a caption…", text: $caption, axis: .vertical)
                    .lineLimit(3...8).disabled(draft.submitted).accessibilityLabel("Optional caption")
                DisclosureGroup("More") {
                    TextField("Optional title", text: $title).disabled(draft.submitted)
                }
            }
            Section("Destination") {
                if let destination = draft.destination {
                    Text(destination.name).font(.headline)
                    Text(destination.ipns).font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                } else if let connection = model.connection {
                    Toggle("Post to \(connection.name)", isOn: $confirmDestination)
                    Text("This image was saved before setup. Confirm where it should be published.")
                        .font(.caption).foregroundStyle(.secondary)
                } else {
                    Label("Saved for later", systemImage: "tray")
                    Text("Open the Croptop app and connect your site. Your screenshot will be waiting in Drafts.")
                        .foregroundStyle(.secondary)
                }
                if model.connection?.enabled == false && !draft.submitted {
                    Text("Enable phone posting in the Croptop app before publishing. Your draft is saved.").font(.caption).foregroundStyle(.secondary)
                }
            }
            Section {
                Label(draft.statusLabel, systemImage: draft.operation?.state == .published ? "checkmark.circle.fill" : "doc")
                if let message = model.message { Text(message).font(.callout).accessibilityIdentifier("publishingStatus") }
                else if let error = draft.lastError { Text(error).font(.callout).foregroundStyle(.secondary) }
                if model.busy { ProgressView("Working…") }
                if draft.operation?.state == .published, let rawURL = draft.operation?.url, let url = URL(string: rawURL) {
                    Link("Open published post", destination: url).buttonStyle(.borderedProminent)
                    #if !SHARE_EXTENSION
                    Button("Copy post link") { UIPasteboard.general.url = url }
                    #endif
                } else if draft.canStartCorrectedDraft {
                    Text(draft.canStartAgainAfterExpiry ? "The service confirmed that this upload expired. Your screenshot is still on this phone." : "This image was rejected before publication. Start a corrected draft and replace the image.")
                    Button("Start a new draft from this image") { model.startNewAfterRejection() }
                        .buttonStyle(.borderedProminent).disabled(model.busy)
                } else if draft.operation?.state == .needsSignature && model.previewImage != nil && !draft.commitIsUnconfirmed {
                    Button("Publish reviewed preview") { Task { await model.publishReviewedPreview() } }
                        .buttonStyle(.borderedProminent).disabled(model.busy)
                    Button("Refresh preview") { Task { await model.refresh() } }.disabled(model.busy)
                } else {
                    Button(draft.submitted ? "Resume / check status" : "Prepare preview") {
                        Task { await model.prepare(title: title, caption: caption, confirmDestination: confirmDestination) }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.busy || model.connection == nil || (!draft.submitted && model.connection?.enabled != true) || (draft.destination == nil && !confirmDestination))
                }
                if let onSaved {
                    Button(draft.operation?.state == .published ? "Done" : (draft.submitted ? "Close — keep pending post" : "Save draft")) {
                        do { try model.save(title: title, caption: caption); onSaved() }
                        catch { model.message = error.localizedDescription }
                    }.disabled(model.busy)
                }
            }
        }
        .navigationTitle(draft.operation?.state == .published ? "Published" : "New post")
        .navigationBarTitleDisplayMode(.inline)
        .onChange(of: title) { _, _ in saveEdits() }
        .onChange(of: caption) { _, _ in saveEdits() }
        .onChange(of: replacementImage) { _, selected in
            let originalDraftID = draft.id
            Task {
                do {
                    guard let image = try await selected?.loadTransferable(type: ScreenshotTransfer.self) else { return }
                    try model.replaceImage(image.data, for: originalDraftID); replacementImage = nil
                } catch { model.message = error.localizedDescription }
            }
        }
    }

    private func saveEdits() {
        do { try model.save(title: title, caption: caption) }
        catch { model.message = error.localizedDescription }
    }
}
