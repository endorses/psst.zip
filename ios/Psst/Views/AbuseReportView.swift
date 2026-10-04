import Shared
import SwiftUI
import UIKit

/// Guest lookups use a new anonymous, bounded config client for the link's exact origin.
struct AbuseReportButton: View {
    let context: AbuseReportContext
    var configuredContact: String? = nil
    var fetchContact = true
    @State private var loadedOrigin: String?
    @State private var contact: String?
    @State private var showing = false
    private var currentContact: String? {
        fetchContact
            ? (loadedOrigin == context.origin ? contact : nil)
            : configuredContact.flatMap(AbuseContact.validated)
    }

    var body: some View {
        Group {
            if let contact = currentContact {
                Button {
                    showing = true
                } label: {
                    Label("Report abuse", systemImage: "flag")
                }
                .sheet(isPresented: $showing) {
                    AbuseReportView(context: context, contact: contact)
                }
            }
        }
        .task(id: context.origin) {
            loadedOrigin = nil
            contact = nil
            guard fetchContact else { return }
            let origin = context.origin
            do {
                let client = try ApiClient.companion.anonymous(origin: origin)
                defer { client.close() }
                let config = try await client.limits.get()
                guard !Task.isCancelled, context.origin == origin else { return }
                contact = AbuseContact.validated(config.abuseContactEmail)
                loadedOrigin = origin
            } catch {
                // Reporting is optional; unavailable or invalid contact never exposes a stale address.
            }
        }
    }
}

private struct AbuseReportView: View {
    let context: AbuseReportContext
    let contact: String
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var feedback: String?
    var body: some View {
        NavigationStack {
            Form {
                Section("Server operator") {
                    Text(contact).textSelection(.enabled)
                    Button("Copy contact address") {
                        UIPasteboard.general.string = contact
                        feedback = "Contact address copied."
                    }
                }
                Section("Report details") {
                    Text(context.text).textSelection(.enabled)
                    Button("Copy report details") {
                        UIPasteboard.general.string = context.text
                        feedback = "Report details copied."
                    }
                    Text(
                        "Only the instance address, resource type and ID are included. Link secrets, encryption keys and file names are excluded. Nothing is sent automatically."
                    ).font(.footnote)
                }
                if let url = context.mailURL(contact: contact) {
                    Section {
                        Button("Write email") {
                            openURL(url) { accepted in
                                if !accepted {
                                    feedback =
                                        "No email app could be opened. Copy the contact address and report details instead."
                                }
                            }
                        }
                        Text(
                            "Review and send the message in your email app. The server operator handles reports; this does not revoke the link."
                        ).font(.footnote)
                    }
                }
                if let feedback {
                    Section { Text(feedback).accessibilityAddTraits(.updatesFrequently) }
                }
            }
            .navigationTitle("Report abuse").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }.modifier(PsstStyle()).modifier(PsstAppearance())
    }
}
