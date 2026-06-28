import AppKit
import SwiftUI

enum WindowPlacement {
    @MainActor
    static func clamp(_ window: NSWindow) {
        let visibleFrames = NSScreen.screens.map(\.visibleFrame)
        let clamped = clampedFrame(window.frame, visibleFrames: visibleFrames)
        guard clamped != window.frame else { return }
        window.setFrame(clamped, display: true)
    }

    static func clampedFrame(_ frame: CGRect, visibleFrames: [CGRect]) -> CGRect {
        guard let visibleFrame = bestVisibleFrame(for: frame, in: visibleFrames) else {
            return frame
        }

        let width = min(frame.width, visibleFrame.width)
        let height = min(frame.height, visibleFrame.height)
        let x = min(max(frame.minX, visibleFrame.minX), visibleFrame.maxX - width)
        let y = min(max(frame.minY, visibleFrame.minY), visibleFrame.maxY - height)
        return CGRect(x: x, y: y, width: width, height: height)
    }

    private static func bestVisibleFrame(for frame: CGRect, in visibleFrames: [CGRect]) -> CGRect? {
        visibleFrames
            .max { lhs, rhs in
                lhs.intersection(frame).area < rhs.intersection(frame).area
            }
            ?? visibleFrames.first
    }
}

private extension CGRect {
    var area: CGFloat {
        guard !isNull, !isEmpty else { return 0 }
        return width * height
    }
}

struct WindowPlacementReader: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView {
        let view = NSView(frame: .zero)
        DispatchQueue.main.async {
            if let window = view.window {
                WindowPlacement.clamp(window)
            }
        }
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        DispatchQueue.main.async {
            if let window = nsView.window {
                WindowPlacement.clamp(window)
            }
        }
    }
}
