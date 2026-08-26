import XCTest
@testable import Maccy

final class ClipboardSyncURLProtocol: URLProtocol {
  static var requestHandler: ((URLRequest) throws -> (HTTPURLResponse, Data))?

  override class func canInit(with request: URLRequest) -> Bool { true }
  override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

  override func startLoading() {
    guard let handler = Self.requestHandler else {
      XCTFail("Missing request handler")
      return
    }

    do {
      let (response, data) = try handler(request)
      client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
      client?.urlProtocol(self, didLoad: data)
      client?.urlProtocolDidFinishLoading(self)
    } catch {
      client?.urlProtocol(self, didFailWithError: error)
    }
  }

  override func stopLoading() {}
}

final class ClipboardSyncTests: XCTestCase {
  private var queueURL: URL!

  override func setUpWithError() throws {
    let directory = FileManager.default.temporaryDirectory
      .appending(path: "MaccyClipboardSyncTests-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    queueURL = directory.appending(path: "queue.json")
  }

  override func tearDownWithError() throws {
    ClipboardSyncURLProtocol.requestHandler = nil
    try? FileManager.default.removeItem(at: queueURL.deletingLastPathComponent())
  }

  @MainActor
  func testSyncSendsConfiguredBatchAndRemovesUploadedEvents() async throws {
    var capturedRequest: URLRequest?
    var capturedBody: Data?
    ClipboardSyncURLProtocol.requestHandler = { request in
      capturedRequest = request
      capturedBody = try Self.bodyData(for: request)
      return Self.response(for: request, statusCode: 200, body: """
        {"accepted":2,"duplicates":0,"last_server_seq":2}
        """)
    }
    let sync = makeSync(batchSize: 2)

    sync.enqueue(makeItem(text: "hello", application: "com.apple.TextEdit"))
    sync.enqueue(makeItem(text: "world"))
    sync.enqueue(makeItem(text: "later"))
    await sync.sync()

    XCTAssertEqual(sync.pendingEventCount, 1)
    XCTAssertEqual(capturedRequest?.url?.absoluteString, "http://127.0.0.1:8234/v1/sync/events:batch")
    XCTAssertEqual(capturedRequest?.value(forHTTPHeaderField: "Authorization"), "Bearer test-secret")
    XCTAssertEqual(capturedRequest?.value(forHTTPHeaderField: "Content-Type"), "application/json")

    let body = try XCTUnwrap(capturedBody)
    let payload = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
    XCTAssertEqual(payload["device_id"] as? String, "test-device")
    let events = try XCTUnwrap(payload["events"] as? [[String: Any]])
    XCTAssertEqual(events.count, 2)
    XCTAssertEqual(events[0]["plain_text"] as? String, "hello")
    XCTAssertEqual(events[0]["source_bundle_id"] as? String, "com.apple.TextEdit")
    XCTAssertEqual(
      events[0]["content_sha256"] as? String,
      "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
    )
    XCTAssertNotNil(events[0]["client_event_id"] as? String)
    XCTAssertNotNil(events[0]["copied_at"] as? String)
  }

  @MainActor
  func testFailedSyncRetainsEventsForRetry() async {
    var attempt = 0
    ClipboardSyncURLProtocol.requestHandler = { request in
      attempt += 1
      if attempt == 1 {
        return Self.response(for: request, statusCode: 500, body: "{\"error\":\"failed\"}")
      }
      return Self.response(for: request, statusCode: 200, body: """
        {"accepted":1,"duplicates":0,"last_server_seq":1}
        """)
    }
    let sync = makeSync()
    sync.enqueue(makeItem(text: "retry me"))

    await sync.sync()
    XCTAssertEqual(sync.pendingEventCount, 1)

    await sync.sync()
    XCTAssertEqual(sync.pendingEventCount, 0)
    XCTAssertEqual(attempt, 2)
  }

  @MainActor
  func testPendingEventsSurviveReinitialization() {
    let sync = makeSync()
    sync.enqueue(makeItem(text: "persist me"))

    let restored = makeSync()

    XCTAssertEqual(restored.pendingEventCount, 1)
  }

  @MainActor
  func testStartSynchronizesPendingEvents() async {
    let requestExpectation = expectation(description: "Sync request is sent")
    ClipboardSyncURLProtocol.requestHandler = { request in
      requestExpectation.fulfill()
      return Self.response(for: request, statusCode: 200, body: """
        {"accepted":1,"duplicates":0,"last_server_seq":1}
        """)
    }
    let sync = makeSync()
    sync.enqueue(makeItem(text: "scheduled"))

    sync.start()
    await fulfillment(of: [requestExpectation], timeout: 1)
    sync.stop()

    XCTAssertEqual(sync.pendingEventCount, 0)
  }

  @MainActor
  func testDisabledSyncDoesNotQueueClipboardText() {
    let sync = makeSync(backendAddress: "", secret: "")

    sync.enqueue(makeItem(text: "local only"))

    XCTAssertEqual(sync.pendingEventCount, 0)
  }

  @MainActor
  private func makeSync(
    backendAddress: String = "http://127.0.0.1:8234",
    secret: String = "test-secret",
    batchSize: Int = 50
  ) -> ClipboardSync {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [ClipboardSyncURLProtocol.self]
    let session = URLSession(configuration: configuration)
    let syncConfiguration = ClipboardSync.Configuration(
      backendAddress: backendAddress,
      secret: secret,
      interval: 20,
      batchSize: batchSize,
      deviceID: "test-device"
    )
    return ClipboardSync(session: session, queueURL: queueURL) { syncConfiguration }
  }

  private func makeItem(text: String, application: String? = nil) -> HistoryItem {
    let content = HistoryItemContent(
      type: NSPasteboard.PasteboardType.string.rawValue,
      value: text.data(using: .utf8)
    )
    let item = HistoryItem(contents: [content])
    item.application = application
    return item
  }

  private static func response(
    for request: URLRequest,
    statusCode: Int,
    body: String
  ) -> (HTTPURLResponse, Data) {
    let response = HTTPURLResponse(
      url: request.url!,
      statusCode: statusCode,
      httpVersion: nil,
      headerFields: ["Content-Type": "application/json"]
    )!
    return (response, Data(body.utf8))
  }

  private static func bodyData(for request: URLRequest) throws -> Data {
    if let body = request.httpBody {
      return body
    }

    guard let stream = request.httpBodyStream else {
      return Data()
    }

    stream.open()
    defer { stream.close() }

    var data = Data()
    var buffer = [UInt8](repeating: 0, count: 4_096)
    while true {
      let count = stream.read(&buffer, maxLength: buffer.count)
      if count < 0 {
        throw stream.streamError ?? URLError(.cannotDecodeRawData)
      }
      if count == 0 {
        return data
      }
      data.append(contentsOf: buffer[..<count])
    }
  }
}
