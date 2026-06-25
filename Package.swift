// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "OCH",
    defaultLocalization: "en",
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
            resources: [
                .process("Resources")
            ],
            linkerSettings: [
                .linkedFramework("Security")
            ]
        )
    ]
)
