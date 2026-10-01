// Package version 版本信息（由构建时 ldflags 注入：
// -X keyhive/internal/version.Version=v1.2.0）
package version

import "runtime"

var (
	Version = "dev"
	Commit  = "none"
)

func String() string {
	s := Version
	if Commit != "none" && Commit != "" {
		s += " (" + Commit + ")"
	}
	return s + " " + runtime.Version()
}
