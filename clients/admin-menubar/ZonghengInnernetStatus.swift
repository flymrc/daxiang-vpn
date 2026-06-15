import AppKit
import Foundation

final class Command {
    static func run(_ launchPath: String, _ arguments: [String] = []) -> (Int32, String) {
        let process = Process()
        let pipe = Pipe()
        process.executableURL = URL(fileURLWithPath: launchPath)
        process.arguments = arguments
        process.standardOutput = pipe
        process.standardError = pipe
        do {
            try process.run()
            process.waitUntilExit()
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            return (process.terminationStatus, String(data: data, encoding: .utf8) ?? "")
        } catch {
            return (127, "\(error)")
        }
    }

    static func shell(_ command: String) -> (Int32, String) {
        run("/bin/sh", ["-c", command])
    }

    static func adminShell(_ command: String) {
        let escaped = command
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
        _ = run("/usr/bin/osascript", [
            "-e",
            "do shell script \"\(escaped)\" with administrator privileges"
        ])
    }
}

final class InnernetStatusApp: NSObject, NSApplicationDelegate {
    private let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let menu = NSMenu()
    private let statusItem = NSMenuItem(title: "Checking...", action: nil, keyEquivalent: "")
    private let endpointItem = NSMenuItem(title: "", action: nil, keyEquivalent: "")
    private let connectItem = NSMenuItem(title: "Connect admin-innernet", action: #selector(connect), keyEquivalent: "c")
    private var timer: Timer?
    private let helperDir = NSHomeDirectory() + "/.zhvpn/bin"

    func applicationDidFinishLaunching(_ notification: Notification) {
        item.button?.title = "ZH ..."
        item.menu = menu
        buildMenu()
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 10, repeats: true) { [weak self] _ in
            self?.refresh()
        }
    }

    private func buildMenu() {
        statusItem.isEnabled = false
        endpointItem.isEnabled = false
        menu.addItem(statusItem)
        menu.addItem(endpointItem)
        menu.addItem(.separator())
        menu.addItem(connectItem)
        menu.addItem(NSMenuItem(title: "Disconnect admin-innernet", action: #selector(disconnect), keyEquivalent: "d"))
        menu.addItem(NSMenuItem(title: "Refresh", action: #selector(refreshAction), keyEquivalent: "r"))
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "Copy Android SSH command", action: #selector(copySSH), keyEquivalent: "s"))
        menu.addItem(NSMenuItem(title: "Open local config folder", action: #selector(openConfigFolder), keyEquivalent: "o"))
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "Quit", action: #selector(quit), keyEquivalent: "q"))
        for item in menu.items where item.action != nil {
            item.target = self
        }
    }

    @objc private func refreshAction() {
        refresh()
    }

    private func refresh() {
        let ifconfig = Command.shell("/sbin/ifconfig").1
        let hasAdmin = ifconfig.contains("10.66.0.40")
        let hasMacEgress = ifconfig.contains("10.66.0.100")
        let pingHub = Command.shell("/sbin/ping -c 1 -W 1000 10.66.0.1 >/dev/null 2>&1").0 == 0
        let pingAndroid = Command.shell("/sbin/ping -c 1 -W 1000 10.66.0.101 >/dev/null 2>&1").0 == 0

        let title: String
        let status: String
        if pingHub && pingAndroid {
            title = "ZH OK"
            status = "Innernet online"
        } else if pingHub {
            title = "ZH Hub"
            status = "Hub reachable, Android not reachable"
        } else {
            title = "ZH Off"
            status = "Innernet offline"
        }

        item.button?.title = title
        statusItem.title = status
        let iface = hasAdmin ? "admin-innernet 10.66.0.40" : (hasMacEgress ? "deprecated mac peer 10.66.0.100" : "no local WG address")
        endpointItem.title = iface
        connectItem.isEnabled = !hasMacEgress
        connectItem.title = hasMacEgress ? "Connect disabled: deprecated Mac peer active" : "Connect admin-innernet"
    }

    @objc private func connect() {
        Command.adminShell(helperScript("zhvpn-admin-innernet-up.sh"))
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { self.refresh() }
    }

    @objc private func disconnect() {
        Command.adminShell(helperScript("zhvpn-admin-innernet-down.sh"))
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { self.refresh() }
    }

    @objc private func copySSH() {
        let command = "ssh -i ~/.ssh/zhandroid_control_local -p 2022 root@10.66.0.101"
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(command, forType: .string)
    }

    @objc private func openConfigFolder() {
        NSWorkspace.shared.open(URL(fileURLWithPath: NSHomeDirectory() + "/.zhvpn/wireguard"))
    }

    private func helperScript(_ name: String) -> String {
        let local = helperDir + "/" + name
        if FileManager.default.isExecutableFile(atPath: local) {
            return local
        }
        return "/usr/local/sbin/" + name
    }

    @objc private func quit() {
        NSApp.terminate(nil)
    }
}

let app = NSApplication.shared
let delegate = InnernetStatusApp()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
