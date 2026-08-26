import CryptoKit
import Defaults
import Foundation
import Logging

@MainActor
final class ClipboardSync {
  struct Configuration {
    let backendAddress: String
    let secret: String
    let interval: Int
    let batchSize: Int
    let deviceID: String

    var isEnabled: Bool {
      !backendAddress.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !secret.isEmpty
    }
  }

  private struct Event: Codable {
    let clientEventID: String
    let copiedAt: Date
    let sourceBundleID: String?
    let plainText: String
    let contentSHA256: String

    enum CodingKeys: String, CodingKey {
      case clientEventID = "client_event_id"
      case copiedAt = "copied_at"
      case sourceBundleID = "source_bundle_id"
      case plainText = "plain_text"
      case contentSHA256 = "content_sha256"
    }
  }

  private struct UploadRequest: Encodable {
    let deviceID: String
    let events: [Event]

    enum CodingKeys: String, CodingKey {
      case deviceID = "device_id"
      case events
    }
  }

  private struct UploadResponse: Decodable {
    let accepted: Int
    let duplicates: Int
  }

  private enum SyncError: Error {
    case invalidResponse
    case httpStatus(Int)
    case incompleteResponse
  }

  static let shared = ClipboardSync(
    session: .shared,
    queueURL: URL.applicationSupportDirectory.appending(path: "Maccy/ClipboardSyncQueue.json"),
    configuration: { currentConfiguration() }
  )

  private static let maxTextBytes = 1 << 20

  private let session: URLSession
  private let queueURL: URL
  private let configuration: () -> Configuration
  private let logger = Logger(label: "org.p0deje.Maccy.ClipboardSync")

  private var queue: [Event]
  private var timerTask: Task<Void, Never>?
  private var isSyncing = false

  var pendingEventCount: Int { queue.count }

  init(session: URLSession, queueURL: URL, configuration: @escaping () -> Configuration) {
    self.session = session
    self.queueURL = queueURL
    self.configuration = configuration

    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .iso8601
    if let data = try? Data(contentsOf: queueURL),
       let events = try? decoder.decode([Event].self, from: data) {
      queue = events
    } else {
      queue = []
    }
  }

  func start() {
    guard timerTask == nil else { return }

    timerTask = Task { [weak self] in
      guard let self else { return }

      while !Task.isCancelled {
        await sync()

        let interval = min(max(configuration().interval, 1), 86_400)
        do {
          try await Task.sleep(nanoseconds: UInt64(interval) * 1_000_000_000)
        } catch {
          return
        }
      }
    }
  }

  func restart() {
    stop()
    start()
  }

  func stop() {
    timerTask?.cancel()
    timerTask = nil
  }

  func enqueue(_ item: HistoryItem) {
    let configuration = configuration()
    guard configuration.isEnabled,
          let text = item.text,
          !text.isEmpty,
          let data = text.data(using: .utf8),
          data.count <= Self.maxTextBytes else {
      return
    }

    let digest = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    queue.append(Event(
      clientEventID: UUID().uuidString,
      copiedAt: item.lastCopiedAt,
      sourceBundleID: item.application,
      plainText: text,
      contentSHA256: digest
    ))
    persistQueue()
  }

  func sync() async {
    guard !isSyncing, !queue.isEmpty else { return }

    let configuration = configuration()
    guard configuration.isEnabled else { return }

    let batchSize = min(max(configuration.batchSize, 1), 100)
    let batch = Array(queue.prefix(batchSize))
    guard let endpoint = endpoint(for: configuration.backendAddress) else {
      logger.warning("Clipboard sync skipped because the backend address is invalid")
      return
    }

    isSyncing = true
    defer { isSyncing = false }

    do {
      var request = URLRequest(url: endpoint)
      request.httpMethod = "POST"
      request.timeoutInterval = 30
      request.setValue("Bearer \(configuration.secret)", forHTTPHeaderField: "Authorization")
      request.setValue("application/json", forHTTPHeaderField: "Content-Type")

      let encoder = JSONEncoder()
      encoder.dateEncodingStrategy = .iso8601
      request.httpBody = try encoder.encode(UploadRequest(deviceID: configuration.deviceID, events: batch))

      let (data, response) = try await session.data(for: request)
      guard let response = response as? HTTPURLResponse else {
        throw SyncError.invalidResponse
      }
      guard (200..<300).contains(response.statusCode) else {
        throw SyncError.httpStatus(response.statusCode)
      }

      let result = try JSONDecoder().decode(UploadResponse.self, from: data)
      guard result.accepted + result.duplicates == batch.count else {
        throw SyncError.incompleteResponse
      }

      let uploadedIDs = Set(batch.map(\.clientEventID))
      queue.removeAll { uploadedIDs.contains($0.clientEventID) }
      persistQueue()
      logger.info("Synchronized \(batch.count) clipboard event(s)")
    } catch {
      logger.warning("Clipboard sync failed; \(batch.count) event(s) retained: \(String(reflecting: error))")
    }
  }

  private static func currentConfiguration() -> Configuration {
    let backendAddress = Defaults[.syncBackendAddress]
    let secret = Defaults[.syncSecret]
    var deviceID = Defaults[.syncDeviceID]

    if !backendAddress.isEmpty, !secret.isEmpty, deviceID.isEmpty {
      deviceID = UUID().uuidString
      Defaults[.syncDeviceID] = deviceID
    }

    return Configuration(
      backendAddress: backendAddress,
      secret: secret,
      interval: Defaults[.syncInterval],
      batchSize: Defaults[.syncBatchSize],
      deviceID: deviceID
    )
  }

  private func endpoint(for backendAddress: String) -> URL? {
    let address = backendAddress.trimmingCharacters(in: .whitespacesAndNewlines)
    guard let url = URL(string: address),
          let scheme = url.scheme?.lowercased(),
          ["http", "https"].contains(scheme),
          url.host != nil else {
      return nil
    }

    return url.appending(path: "v1/sync/events:batch")
  }

  private func persistQueue() {
    do {
      let encoder = JSONEncoder()
      encoder.dateEncodingStrategy = .iso8601
      let data = try encoder.encode(queue)
      try FileManager.default.createDirectory(
        at: queueURL.deletingLastPathComponent(),
        withIntermediateDirectories: true
      )
      try data.write(to: queueURL, options: .atomic)
      try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: queueURL.path)
    } catch {
      logger.error("Failed to persist the clipboard sync queue: \(String(reflecting: error))")
    }
  }
}
