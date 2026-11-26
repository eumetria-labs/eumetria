package main

// appVersion is the release this binary was built from: "1.2.0" for a tagged
// release, "main-<commit>" for an image built from main, "dev" for a local
// build. The image build sets it (Dockerfile ARG VERSION):
//
//	go build -ldflags "-X main.appVersion=1.2.0" ./cmd/eumetria
//
// Only admins see it (Settings → General), and -version prints it.
var appVersion = "dev"
