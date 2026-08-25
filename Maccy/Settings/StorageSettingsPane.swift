import SwiftUI
import Defaults
import Settings

struct StorageSettingsPane: View {
  @Observable
  class ViewModel {
    var saveFiles = false {
      didSet {
        Defaults.withoutPropagation {
          if saveFiles {
            Defaults[.enabledPasteboardTypes].formUnion(StorageType.files.types)
          } else {
            Defaults[.enabledPasteboardTypes].subtract(StorageType.files.types)
          }
        }
      }
    }

    var saveImages = false {
      didSet {
        Defaults.withoutPropagation {
          if saveImages {
            Defaults[.enabledPasteboardTypes].formUnion(StorageType.images.types)
          } else {
            Defaults[.enabledPasteboardTypes].subtract(StorageType.images.types)
          }
        }
      }
    }

    var saveText = false {
      didSet {
        Defaults.withoutPropagation {
          if saveText {
            Defaults[.enabledPasteboardTypes].formUnion(StorageType.text.types)
          } else {
            Defaults[.enabledPasteboardTypes].subtract(StorageType.text.types)
          }
        }
      }
    }

    private var observer: Defaults.Observation?

    init() {
      observer = Defaults.observe(.enabledPasteboardTypes) { change in
        self.saveFiles = change.newValue.isSuperset(of: StorageType.files.types)
        self.saveImages = change.newValue.isSuperset(of: StorageType.images.types)
        self.saveText = change.newValue.isSuperset(of: StorageType.text.types)
      }
    }

    deinit {
      observer?.invalidate()
    }
  }

  @Default(.size) private var size
  @Default(.sortBy) private var sortBy
  @Default(.syncBackendAddress) private var syncBackendAddress
  @Default(.syncBatchSize) private var syncBatchSize
  @Default(.syncInterval) private var syncInterval
  @Default(.syncSecret) private var syncSecret

  @State private var viewModel = ViewModel()
  @State private var storageSize = Storage.shared.size

  private let sizeFormatter: NumberFormatter = {
    let formatter = NumberFormatter()
    formatter.minimum = 1
    formatter.maximum = 999
    return formatter
  }()

  private let syncIntervalFormatter: NumberFormatter = {
    let formatter = NumberFormatter()
    formatter.minimum = 1
    formatter.maximum = 86_400
    return formatter
  }()

  private let syncBatchSizeFormatter: NumberFormatter = {
    let formatter = NumberFormatter()
    formatter.minimum = 1
    formatter.maximum = 100
    return formatter
  }()

  var body: some View {
    Settings.Container(contentWidth: 450) {
      Settings.Section(
        bottomDivider: true,
        label: { Text("Save", tableName: "StorageSettings") }
      ) {
        Toggle(
          isOn: $viewModel.saveFiles,
          label: { Text("Files", tableName: "StorageSettings") }
        )
        Toggle(
          isOn: $viewModel.saveImages,
          label: { Text("Images", tableName: "StorageSettings") }
        )
        Toggle(
          isOn: $viewModel.saveText,
          label: { Text("Text", tableName: "StorageSettings") }
        )
        Text("SaveDescription", tableName: "StorageSettings")
          .controlSize(.small)
          .foregroundStyle(.gray)
      }

      Settings.Section(label: { Text("Size", tableName: "StorageSettings") }) {
        HStack {
          TextField("", value: $size, formatter: sizeFormatter)
            .frame(width: 80)
            .help(Text("SizeTooltip", tableName: "StorageSettings"))
            .accessibilityLabel(Text("Size", tableName: "StorageSettings"))
          Stepper("", value: $size, in: 1...999)
            .labelsHidden()
            .accessibilityLabel(Text("Size", tableName: "StorageSettings"))
          Text(storageSize)
            .controlSize(.small)
            .foregroundStyle(.gray)
            .help(Text("CurrentSizeTooltip", tableName: "StorageSettings"))
            .onAppear {
              storageSize = Storage.shared.size
            }
        }
      }

      Settings.Section(
        bottomDivider: true,
        label: { Text("SortBy", tableName: "StorageSettings") }
      ) {
        Picker("", selection: $sortBy) {
          ForEach(Sorter.By.allCases) { mode in
            Text(mode.description)
          }
        }
        .labelsHidden()
        .frame(width: 160, alignment: .leading)
        .help(Text("SortByTooltip", tableName: "StorageSettings"))
        .accessibilityLabel(Text("SortBy", tableName: "StorageSettings"))
      }

      Settings.Section(label: { Text("Backend address:", tableName: "StorageSettings") }) {
        TextField("https://maccy.example.com", text: $syncBackendAddress)
          .frame(width: 260)
          .help(Text("Base URL of the clipboard sync server.", tableName: "StorageSettings"))
          .accessibilityLabel(Text("Backend address", tableName: "StorageSettings"))
      }

      Settings.Section(label: { Text("Secret:", tableName: "StorageSettings") }) {
        SecureField("", text: $syncSecret)
          .frame(width: 260)
          .help(Text("Bearer secret configured on the server.", tableName: "StorageSettings"))
          .accessibilityLabel(Text("Secret", tableName: "StorageSettings"))
          .privacySensitive()
      }

      Settings.Section(label: { Text("Sync interval:", tableName: "StorageSettings") }) {
        HStack {
          TextField("", value: $syncInterval, formatter: syncIntervalFormatter)
            .frame(width: 80)
            .help(Text("How often clipboard data is synchronized.", tableName: "StorageSettings"))
            .accessibilityLabel(Text("Sync interval", tableName: "StorageSettings"))
          Stepper("", value: $syncInterval, in: 1...86_400)
            .labelsHidden()
            .accessibilityLabel(Text("Sync interval", tableName: "StorageSettings"))
          Text("seconds", tableName: "StorageSettings")
            .foregroundStyle(.gray)
        }
      }

      Settings.Section(label: { Text("Batch size:", tableName: "StorageSettings") }) {
        HStack {
          TextField("", value: $syncBatchSize, formatter: syncBatchSizeFormatter)
            .frame(width: 80)
            .help(Text("Number of clipboard events sent in each request.", tableName: "StorageSettings"))
            .accessibilityLabel(Text("Batch size", tableName: "StorageSettings"))
          Stepper("", value: $syncBatchSize, in: 1...100)
            .labelsHidden()
            .accessibilityLabel(Text("Batch size", tableName: "StorageSettings"))
        }
      }
    }
  }
}

#Preview {
  StorageSettingsPane()
    .environment(\.locale, .init(identifier: "en"))
}
