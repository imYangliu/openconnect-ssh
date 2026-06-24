// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "OCH",
    platforms: [
        .macOS(.v13)
    ],
    products: [
        .executable(name: "OCHApp", targets: ["OCHApp"])
    ],
    targets: [
        .executableTarget(
            name: "OCHApp",
            path: "Sources/OCHApp",
            linkerSettings: [
                .linkedFramework("Security")
            ]
        )
    ]
)
