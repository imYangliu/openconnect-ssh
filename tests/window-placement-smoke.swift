import AppKit
import Foundation

@main
struct WindowPlacementSmoke {
    static func main() {
        let visible = CGRect(x: 0, y: 0, width: 1440, height: 900)
        let offscreen = CGRect(x: -157, y: -856, width: 900, height: 699)
        let clamped = WindowPlacement.clampedFrame(offscreen, visibleFrames: [visible])

        expect(clamped.minX >= visible.minX, "clamped x is visible")
        expect(clamped.minY >= visible.minY, "clamped y is visible")
        expect(clamped.maxX <= visible.maxX, "clamped max x is visible")
        expect(clamped.maxY <= visible.maxY, "clamped max y is visible")

        let onscreen = CGRect(x: 100, y: 100, width: 900, height: 699)
        expect(
            WindowPlacement.clampedFrame(onscreen, visibleFrames: [visible]) == onscreen,
            "onscreen frame is unchanged"
        )

        print("window placement smoke passed")
    }

    private static func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
        if !condition() {
            FileHandle.standardError.write(Data("window placement smoke failed: \(message)\n".utf8))
            exit(1)
        }
    }
}
