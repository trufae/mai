// swift-tools-version: 5.9
import PackageDescription

let package = Package(
  name: "MaiUI",
  platforms: [
    .macOS(.v13)
  ],
  dependencies: [],
  targets: [
    .executableTarget(
      name: "MaiUI",
      dependencies: []
    )
  ]
)
