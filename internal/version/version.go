// Package version holds the build's version: `git describe --tags --long`,
// i.e. <tag>-<commits since the tag>-g<commit>. Set at link time.
package version

// Version is "dev" when built without the linker flag.
var Version = "dev"
