//
//  MaiUIApp.swift
//  MaiUI
//
//  Created by MaiUI on 2025-01-11.
//

import SwiftUI
#if os(macOS)
import AppKit
#endif

@main
struct MaiUIApp: App {
    @AppStorage("theme") private var theme = "system"

    init() {
        print("MaiUIApp init")
        #if os(macOS)
        NSApplication.shared.setActivationPolicy(.regular)
        DispatchQueue.main.async {
            print("MaiUIApp activating application")
            NSApplication.shared.activate(ignoringOtherApps: true)
            NSApplication.shared.windows.forEach { window in
                print("MaiUIApp ordering front existing window: \(window.title)")
                window.makeKeyAndOrderFront(nil)
            }
        }
        #endif
    }

    var body: some Scene {
        print("MaiUIApp body invoked with theme: \(theme)")
        return WindowGroup {
            ContentView()
                .onAppear {
                    print("ContentView appeared from MaiUIApp")
                    #if os(macOS)
                    DispatchQueue.main.async {
                        if let window = NSApplication.shared.windows.first {
                            print("ContentView ensuring window is key and visible")
                            window.makeKeyAndOrderFront(nil)
                            NSApplication.shared.activate(ignoringOtherApps: true)
                        } else {
                            print("ContentView did not find window to activate")
                        }
                    }
                    #endif
                }
                .preferredColorScheme(colorScheme)
        }
        .windowStyle(.hiddenTitleBar)
        .windowToolbarStyle(.unifiedCompact)
    }

    private var colorScheme: ColorScheme? {
        let scheme: ColorScheme?
        switch theme {
        case "light":
            scheme = .light
        case "dark":
            scheme = .dark
        default:
            scheme = nil
        }
        print("MaiUIApp resolved colorScheme: \(String(describing: scheme))")
        return scheme
    }
}
