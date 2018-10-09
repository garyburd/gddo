// Copyright 2013 The Go Authors. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file or at
// https://developers.google.com/open-source/licenses/bsd.

//go:generate go run gen.go -output data.go

package gosrc

import (
	"path"
	"regexp"
	"strings"
)

var validHost = regexp.MustCompile(`^[-a-z0-9]+(?:\.[-a-z0-9]+)+$`)
var validPathElement = regexp.MustCompile(`^[-\p{L}0-9~+_][-\p{L}0-9_.]*$`)

func isValidPathElement(s string) bool {
	return validPathElement.MatchString(s)
}

// IsValidRemotePath returns true if importPath is structurally valid for "go get".
func IsValidRemotePath(importPath string) bool {

	parts := strings.Split(importPath, "/")

	if !validTLDs[path.Ext(parts[0])] {
		return false
	}

	// use only the hostname, if there is a username in the package name
	hostparts := strings.Split(parts[0], "@")
	host := hostparts[0]
	if len(hostparts) > 1 {
		host = hostparts[1]
	}

	if !validHost.MatchString(host) {
		return false
	}

	for _, part := range parts[1:] {
		if !isValidPathElement(part) {
			return false
		}
	}

	return true
}

// IsGoRepoPath returns true if path is in $GOROOT/src.
func IsGoRepoPath(path string) bool {
	return pathFlags[path]&goRepoPath != 0
}

// IsValidPath returns true if importPath is structurally valid.
func IsValidPath(importPath string) bool {
	return pathFlags[importPath]&packagePath != 0 ||
		pathFlags["vendor/"+importPath]&packagePath != 0 ||
		IsValidRemotePath(importPath)
}
