// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "ReplicantMenu",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "ReplicantMenu",
            path: "Sources/ReplicantMenu",
            swiftSettings: [.unsafeFlags(["-parse-as-library"])]
        )
    ]
)
