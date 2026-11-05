import SwiftUI
import UIKit

// EmbedBench: the local-embedder bench on an iPhone. Launched by
// `devicectl device process launch --console … -- <args>`; runs one bench
// on a background thread, prints RESULT json to stdout, writes the outputs
// under Documents/out/, then exits. Launched without args it stays idle.

@main
struct EmbedBenchApp: App {
    @StateObject private var state = BenchState()
    var body: some Scene {
        WindowGroup {
            VStack(alignment: .leading, spacing: 8) {
                Text("EmbedBench").font(.headline)
                ScrollView {
                    Text(state.log)
                        .font(.system(size: 10, design: .monospaced))
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .padding()
            .onAppear { state.start() }
        }
    }
}

final class BenchState: ObservableObject {
    @Published var log = ""
    private var started = false

    func start() {
        guard !started else { return }
        started = true
        let args = Array(CommandLine.arguments.dropFirst())
        guard !args.isEmpty else {
            log = "idle: launch with args"
            return
        }
        UIApplication.shared.isIdleTimerDisabled = true
        UIDevice.current.isBatteryMonitoringEnabled = true
        let t = Thread {
            let code = benchMain(args: args) { line in
                print(line)
                fflush(stdout)
                DispatchQueue.main.async { self.log += line + "\n" }
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 1) { exit(code) }
        }
        t.stackSize = 16 << 20
        t.qualityOfService = .userInitiated
        t.start()
    }
}
