// Package version expose la version de l'agent, injectée à la compilation.
package version

// Version est remplacée par GoReleaser (-ldflags "-X ...version.Version=1.2.3").
var Version = "dev"
