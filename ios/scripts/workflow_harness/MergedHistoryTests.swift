import Foundation
import XCTest

@testable import HistoryPageHarness

final class MergedHistoryTests: XCTestCase {
    @MainActor
    func testSourceFilterRunsBeforePaginationAndNeverReadsUnneededSource() async {
        let history = TransferHistoryStore()
        let guests = GuestDownloadStore()
        let session = DeviceSession(serverURL: "https://a.test", userID: "a")
        for i in 0..<125 {
            history.rows.append(
                TransferRecord(
                    id: "s\(i)", direction: .sent, state: .complete, createdAt: Date(timeIntervalSince1970: Double(i)), fileCount: 1, totalSize: 1, shareURL: nil,
                    serverURL: session.serverURL, ownerID: session.userID, isSlot: i < 100))
        }
        guests.rows = [GuestDownload(id: "g", createdAt: Date())]
        let vm = MergedDeviceHistoryViewModel()
        vm.first(history: history, guests: guests, session: session, filter: .sent)
        XCTAssertEqual(vm.records.count, 25)
        XCTAssertEqual(history.calls, [["transfer"]])
        XCTAssertEqual(guests.calls, 0)
        vm.first(history: history, guests: guests, session: session, filter: .downloaded)
        XCTAssertEqual(vm.records.count, 1)
        XCTAssertEqual(history.calls.count, 1)
        XCTAssertEqual(guests.calls, 1)
        vm.first(history: history, guests: guests, session: session, filter: .receive)
        XCTAssertEqual(vm.records.count, 50)
        XCTAssertEqual(history.calls.last, ["slot"])
        XCTAssertEqual(guests.calls, 1)
        vm.more(history: history, guests: guests)
        XCTAssertEqual(vm.records.count, 100)
        XCTAssertFalse(vm.hasMore)
    }

    @MainActor
    func testAllUsesOneListPreservesFailureAndLogoutRemovesOwnedRows() async {
        let history = TransferHistoryStore()
        let guests = GuestDownloadStore()
        let session = DeviceSession(serverURL: "https://a.test", userID: "a")
        history.rows = [
            TransferRecord(
                id: "s", direction: .sent, state: .complete, createdAt: Date(timeIntervalSince1970: 2), fileCount: 1, totalSize: 1, shareURL: nil, serverURL: session.serverURL,
                ownerID: session.userID)
        ]
        guests.rows = [GuestDownload(id: "g", createdAt: Date(timeIntervalSince1970: 3))]
        let vm = MergedDeviceHistoryViewModel()
        vm.first(history: history, guests: guests, session: session, filter: .all)
        XCTAssertEqual(vm.records.map(\.date), [Date(timeIntervalSince1970: 3), Date(timeIntervalSince1970: 2)])
        guests.fail = true
        vm.first(history: history, guests: guests, session: session, filter: .all)
        XCTAssertEqual(vm.records.count, 2)
        XCTAssertNotNil(vm.error)
        vm.first(history: history, guests: guests, session: nil, filter: .all)
        XCTAssertTrue(vm.records.isEmpty)  // A failed new scope must never retain the previous account.
        guests.fail = false
        vm.first(history: history, guests: guests, session: nil, filter: .all)
        XCTAssertEqual(vm.records.map(\.id), ["download|g"])
    }
    @MainActor
    func testReturningFromDetailsPreservesLoadedWindowWithoutRefetchingSources() async {
        let history = TransferHistoryStore()
        let guests = GuestDownloadStore()
        guests.rows = (0..<151).map { GuestDownload(id: "g\($0)", createdAt: Date(timeIntervalSince1970: Double($0))) }
        let vm = MergedDeviceHistoryViewModel()
        vm.first(history: history, guests: guests, session: nil, filter: .all)
        vm.more(history: history, guests: guests)
        vm.more(history: history, guests: guests)
        let ids = vm.records.map(\.id)
        let calls = guests.calls
        XCTAssertTrue(vm.trimmed)
        XCTAssertEqual(ids.count, 100)
        vm.resume(history: history, guests: guests, session: nil, filter: .all)
        XCTAssertEqual(vm.records.map(\.id), ids)
        XCTAssertEqual(guests.calls, calls)
        guests.rows.removeAll { $0.id == "g100" }
        vm.resume(history: history, guests: guests, session: nil, filter: .all)
        XCTAssertFalse(vm.records.map(\.id).contains("download|g100"))
        vm.first(history: history, guests: guests, session: nil, filter: .all)
        XCTAssertEqual(vm.records.first?.id, "download|g150")
        XCTAssertEqual(vm.records.count, 50)
    }

    @MainActor
    func testIdenticalTimestampsKeepIndexedIdentityOrderAcrossSourcePages() async {
        let history = TransferHistoryStore()
        let guests = GuestDownloadStore()
        let session = DeviceSession(serverURL: "https://a.test", userID: "a")
        let date = Date(timeIntervalSince1970: 42)
        history.rows = (0..<120).reversed().map { index in
            TransferRecord(
                id: String(format: "%03d", index), direction: .sent, state: .complete, createdAt: date, fileCount: 1, totalSize: 1, shareURL: nil, serverURL: session.serverURL,
                ownerID: session.userID, isSlot: index % 2 == 0)
        }
        guests.rows = (0..<75).reversed().map { GuestDownload(id: String(format: "%03d", $0), createdAt: date) }
        let expected = (history.rows.map(HistoryEntry.account) + guests.rows.map(HistoryEntry.downloaded)).sorted { $0.localOrderKey < $1.localOrderKey }.map(\.id)
        let vm = MergedDeviceHistoryViewModel()
        vm.first(history: history, guests: guests, session: session, filter: .all)
        var actual = vm.records.map(\.id)
        while vm.hasMore {
            vm.more(history: history, guests: guests)
            actual += vm.records.suffix(min(50, expected.count - actual.count)).map(\.id)
        }
        XCTAssertEqual(actual, expected)
        XCTAssertEqual(history.calls.count, 3)
        XCTAssertEqual(guests.calls, 2)
    }

}
