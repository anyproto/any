// swift-tools-version:6.2
// The `any` server, packaged as a precompiled iOS binary framework for the
// Swift app to embed in-process (IOS-6169). The xcframework is built and
// published by .github/workflows/xcframework.yml — see Phase 1/2 of the
// embed plan. swift-tools-version must be 6.2+ for the `.iOS(.v26)` platform
// literal (6.0 rejects `v26`).
import PackageDescription

let package = Package(
    name: "AnyServer",
    platforms: [
        .iOS(.v26),
    ],
    products: [
        // A static `.a` xcframework links directly into the consuming app's
        // main binary (no embedded dylib/framework) — verified in spike 0.5.
        .library(name: "AnyServer", targets: ["AnyServer"]),
    ],
    targets: [
        // The `url` and `checksum` below are REWRITTEN by the release
        // workflow on every dispatch and track the latest published release.
        // The committed placeholder does not resolve on its own — resolution
        // works at a release tag, whose commit carries the real values.
        .binaryTarget(
            name: "AnyServer",
            url: "https://github.com/anyproto/any/releases/download/0.1.0/any.xcframework.zip",
            checksum: "0e617633e93fa6e7bbbf583eb995ad357e963c82016c62bf6b754796c6a32e82"
        ),
    ]
)
