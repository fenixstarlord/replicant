// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "ShelfMenu",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "ShelfMenu",
            path: "Sources/ShelfMenu",
            swiftSettings: [.unsafeFlags(["-parse-as-library"])]
        )
    ]
)
