package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set at build time, e.g.
//
//	go build -ldflags "-X main.version=v1.0.0 -X main.commit=abc1234 -X main.date=2026-01-01" ./cmd/vramlens
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// versionString describes the build. Without ldflags it falls back to the
// module and VCS information embedded by the Go toolchain.
func versionString() string {
	v, c, d := version, commit, date
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version // go install ...@vX.Y.Z
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if c == "" && len(s.Value) >= 7 {
					c = s.Value[:7]
				}
			case "vcs.time":
				if d == "" {
					d = s.Value
				}
			}
		}
	}
	out := "vramlens " + v
	if c != "" {
		out += " (" + c
		if d != "" {
			out += ", " + d
		}
		out += ")"
	}
	return fmt.Sprintf("%s %s/%s %s", out, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
