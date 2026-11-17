// Package buildinfo contains release identity injected by official builds.
package buildinfo

// Version is the composed {source}+{ex} identity; -ldflags -X still wins.
var Version = generatedVersion
var SourceCommit = "unknown"
