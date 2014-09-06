// Copyright 2013 The Go Authors. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file or at
// https://developers.google.com/open-source/licenses/bsd.

package gosrc

import (
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

func init() {
	addService(&service{
		pattern: regexp.MustCompile(`^(?P<scope>[a-z0-9A-Z_.\-]+)(?P<host>\.googlesource\.com)(?P<rest>.*)$`),
		prefix:  "",
		get:     getGoogleSourceDir,
	})
}

// See https://code.google.com/p/gitiles/issues/detail?id=7
// Please get rid of this function as soon as this issue is fixed.
func getGitilesRawFile(c *httpClient, baseURL, commitHash, relPath string) ([]byte, error) {
	u := baseURL + "+/" + commitHash + relPath
	content, err := c.getBytes(u)
	if err != nil {
		return nil, err
	}
	// Ugh.
	s := string(content)
	key := "<ol class=\"prettyprint\">"
	i := strings.Index(s, key)
	if i == -1 {
		return nil, errors.New("Unexpected gitiles format")
	}
	s = s[i+len(key):]
	i = strings.Index(s, "</ol>")
	if i == -1 {
		return nil, errors.New("Unexpected gitiles format")
	}
	s = s[:i]
	// Please forgive me.
	s = strings.Replace(s, "</li>", "\n", -1)
	s = regexp.MustCompile("<[^>]+>").ReplaceAllLiteralString(s, "")
	s = html.UnescapeString(s)
	return []byte(s), nil
}

func getGoogleSourceDir(client *http.Client, match map[string]string, savedEtag string) (*Directory, error) {
	baseURL := "https://" + match["scope"] + match["host"] + match["rest"]
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/")
	baseURL = "https://" + u.Host + u.Path + "/"

	c := &httpClient{client: client}

	// Iterate until we find the repo root.
	relPath := ""
	var refs *map[string]struct {
		Value  string `json:"value"`
		Target string `json:"target"`
	}
	for strings.Count(u.Path, "/") != 0 {
		if _, err = c.getNonExecutableJSON(baseURL+"/+refs?format=JSON", &refs); err == nil {
			// Success
			baseURL += "/"
			relPath += "/"
			break
		}
		index := strings.LastIndex(u.Path, "/")
		relPath = u.Path[index:] + relPath
		u.Path = u.Path[:index]
		baseURL = "https://" + u.Host + u.Path
	}
	if err != nil {
		return nil, err
	}

	// Select a commit.
	branchCommit := ""
	branch := "master"
	if v, ok := (*refs)["refs/heads/master"]; ok {
		branchCommit = v.Value
	} else if v, ok := (*refs)["refs/heads/go1"]; ok {
		branchCommit = v.Value
		branch = "go1"
	} else if v, ok := (*refs)["HEAD"]; ok {
		branchCommit = v.Value
		branch = "HEAD"
	} else {
		return nil, errors.New("Failed to find master, go1 or HEAD")
	}

	// Get the files.
	var rawFiles *struct {
		Id      string `json:"id"`
		Entries []struct {
			Mode int    `json:"mode"`
			Type string `json:"type"`
			Id   string `json:"id"`
			Name string `json:"name"`
		}
	}
	if _, err := c.getNonExecutableJSON(baseURL+"+/"+branch+relPath+"?format=JSON", &rawFiles); err != nil {
		return nil, err
	}
	files := []*File{}
	subdirs := []string{}
	for _, rawFile := range rawFiles.Entries {
		if rawFile.Type == "tree" {
			subdirs = append(subdirs, rawFile.Name)
		} else if rawFile.Type == "blob" && isDocFile(rawFile.Name) {
			content, err := getGitilesRawFile(c, baseURL, branchCommit, relPath+rawFile.Name)
			if err != nil {
				return nil, err
			}
			files = append(files, &File{
				Name:      rawFile.Name,
				Data:      content,
				BrowseURL: baseURL + "+/" + branch + relPath + rawFile.Name,
			})
		}
	}
	return &Directory{
		BrowseURL:      baseURL + "+/" + branch + relPath,
		Etag:           branchCommit,
		Files:          files,
		LineFmt:        "%s#%d",
		ImportPath:     u.Host + u.Path + relPath,
		ProjectName:    u.Path,
		ProjectRoot:    u.Host + u.Path,
		ProjectURL:     baseURL + relPath,
		Subdirectories: subdirs,
		VCS:            "git",
	}, nil
}
