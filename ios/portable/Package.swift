// swift-tools-version: 6.2
import PackageDescription

/// Sources.tsv stages exact app/extension sources and original XCTest files.
/// SwiftPM rejects overlapping source paths across targets with different platform
/// boundaries. No source bodies, imports or manifests are generated or rewritten.
let package = Package(
    name: "PsstPortable",
    defaultLocalization: "en",
    platforms: [.macOS(.v15)],
    dependencies: [
        .package(url: "https://github.com/apple/swift-crypto.git", exact: "5.0.1"),
    ],
    targets: [
        .systemLibrary(name: "SQLite3", path: "SQLite3", pkgConfig: "sqlite3"),
        .target(
            name: "Shared",
            path: "Sources/SharedBoundary",
            sources: ["SharedBoundary.swift", "GuestSharedBoundary.swift"],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .target(
            name: "FoundationHarness",
            dependencies: ["Shared", .product(name: "Crypto", package: "swift-crypto")],
            sources: [
                "AbuseReport.swift",
                "AppConstants.swift",
                "Boundaries.swift",
                "ClientErrorPresentation.swift",
                "GuestUploadSelection.swift",
                "HistorySnapshot.swift",
                "HistorySync.swift",
                "InboxPageWindow.swift",
                "LinkLimit.swift",
                "LocalUploadFailure.swift",
                "Localization.swift",
                "ReceiveCrypto.swift",
                "ReceiveSafety.swift",
                "ReleaseSource.swift",
                "ScannerRequestLifetime.swift",
                "SharedLinkTitle.swift",
                "TransferIncident.swift",
                "TransferRecord.swift",
            ],
            resources: [.copy("en.lproj"), .copy("de.lproj")],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "FoundationHarnessTests",
            dependencies: ["FoundationHarness", "Shared"],
            sources: [
                "AbuseReportTests.swift",
                "GuestUploadSelectionTests.swift",
                "HistorySnapshotTests.swift",
                "HistorySyncTests.swift",
                "InboxPageWindowTests.swift",
                "LinkLimitTests.swift",
                "LocalizationTests.swift",
                "ReceiveCryptoTests.swift",
                "ReceiveSafetyTests.swift",
                "ReleaseSourceTests.swift",
                "ScannerRequestLifetimeTests.swift",
                "SharedFailureBridgeTests.swift",
                "TransferIncidentTests.swift",
            ],
            resources: [.copy("history-sync-v1.json"), .copy("hpke-receive-v2.json")],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .target(
            name: "HistoryDatabaseHarness",
            dependencies: ["SQLite3"],
            sources: [
                "DeviceRetryQueue.swift",
                "HistoryJSONStream.swift",
                "HistoryRecordDatabase.swift",
                "ReceiveHistoryStream.swift",
            ],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "HistoryDatabaseHarnessTests",
            dependencies: ["HistoryDatabaseHarness", "SQLite3"],
            sources: [
                "DeviceRetryQueueTests.swift",
                "HistoryRecordDatabaseTests.swift",
            ],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .target(
            name: "HistoryPageHarness",
            dependencies: [],
            sources: [
                "AppConstants.swift",
                "Boundaries.swift",
                "BoundedHistoryMerge.swift",
                "HistorySnapshot.swift",
                "HistoryStores.swift",
                "HistorySync.swift",
                "InboxPageWindow.swift",
                "Localization.swift",
                "MergedDeviceHistoryViewModel.swift",
                "SharedLinkTitle.swift",
                "TransferRecord.swift",
                "UnifiedHistory.swift",
            ],
            resources: [.copy("en.lproj"), .copy("de.lproj")],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "HistoryPageHarnessTests",
            dependencies: ["HistoryPageHarness"],
            sources: [
                "MergedHistoryTests.swift",
                "WorkflowPresentationTests.swift",
            ],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .target(
            name: "HistorySyncHarness",
            dependencies: ["SQLite3"],
            sources: [
                "AppConstants.swift",
                "Boundaries.swift",
                "HistoryCacheIntegration.swift",
                "HistoryCooldowns.swift",
                "HistoryJSONStream.swift",
                "HistoryPageViewModel.swift",
                "HistoryRecordDatabase.swift",
                "HistoryRefresh.swift",
                "HistorySnapshot.swift",
                "HistorySync.swift",
                "InboxPageWindow.swift",
                "Localization.swift",
                "ReceiveHistoryStream.swift",
                "ServerTimestamp.swift",
                "SharedLinkTitle.swift",
                "TransferRecord.swift",
            ],
            resources: [.copy("en.lproj"), .copy("de.lproj")],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "HistorySyncHarnessTests",
            dependencies: ["HistorySyncHarness"],
            sources: [
                "HistorySyncModelTests.swift",
            ],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .target(
            name: "GuestStoreHarness",
            dependencies: ["Shared", "SQLite3", .product(name: "Crypto", package: "swift-crypto")],
            sources: [
                "AppConstants.swift",
                "BoundaryStubs.swift",
                "GuestDownloadStore.swift",
                "GuestHistoryPageViewModel.swift",
                "HistoryJSONStream.swift",
                "HistoryRecordDatabase.swift",
                "Localization.swift",
                "ReceiveHistoryStream.swift",
            ],
            resources: [.copy("en.lproj"), .copy("de.lproj")],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "GuestStoreHarnessTests",
            dependencies: ["GuestStoreHarness", "SQLite3"],
            sources: [
                "GuestStoreTests.swift",
            ],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .target(
            name: "ReceiveCheckpointHarness",
            dependencies: ["Shared", "SQLite3", .product(name: "Crypto", package: "swift-crypto")],
            sources: [
                "BoundaryStubs.swift",
                "DeviceHistoryPageViewModel.swift",
                "HistoryCacheIntegration.swift",
                "HistoryJSONStream.swift",
                "HistoryRecordDatabase.swift",
                "HistoryRefresh.swift",
                "HistorySnapshot.swift",
                "HistorySync.swift",
                "Localization.swift",
                "ReceiveCheckpoint.swift",
                "ReceiveCheckpointStorage.swift",
                "ReceiveHistoryStream.swift",
                "ServerTimestamp.swift",
                "SharedLinkTitle.swift",
                "TransferHistoryStore.swift",
                "TransferIncident.swift",
                "TransferRecord.swift",
            ],
            resources: [.copy("en.lproj"), .copy("de.lproj")],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "ReceiveCheckpointHarnessTests",
            dependencies: ["ReceiveCheckpointHarness", "SQLite3"],
            sources: [
                "ReceiveCheckpointTests.swift",
            ],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
    ]
)
