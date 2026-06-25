// swift-tools-version: 6.2

import PackageDescription

let package = Package(
    name: "OCH",
    defaultLocalization: "en",
    platforms: [
        .macOS(.v26)
    ],
    products: [
        .executable(name: "OCHApp", targets: ["OCHApp"]),
        .executable(name: "OCHPrivilegedHelper", targets: ["OCHPrivilegedHelper"])
    ],
    targets: [
        .target(
            name: "OCHXPCRequirement",
            path: "Sources/OCHXPCRequirement"
        ),
        .target(
            name: "OCHXPCClient",
            dependencies: ["OCHXPCRequirement"],
            path: "Sources/OCHXPCClient"
        ),
        .executableTarget(
            name: "OCHApp",
            dependencies: ["OCHXPCClient"],
            path: "Sources/OCHApp",
            resources: [
                .process("Resources")
            ],
            linkerSettings: [
                .linkedFramework("Security"),
                .linkedFramework("ServiceManagement")
            ]
        ),
        .executableTarget(
            name: "OCHPrivilegedHelper",
            dependencies: ["OCHXPCRequirement"],
            path: "Sources/OCHPrivilegedHelper"
        )
    ]
)
