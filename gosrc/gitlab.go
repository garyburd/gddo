// Copyright 2019 The Go Authors. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file or at
// https://developers.google.com/open-source/licenses/bsd.

package gosrc

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func init() {
	addService(&service{
		pattern:    regexp.MustCompile(`^gitlab\.com/(?P<namespace>[a-z0-9A-Z_.\-]+)/(?P<project>[a-z0-9A-Z_.\-]+)(?P<dir>/[a-z0-9A-Z_.\-/]*)?$`),
		prefix:     "gitlab.com/",
		get:        getGitLabDir,
		getProject: getGitLabProject,
	})
}

type glProject struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	DefaultBranch string    `json:"default_branch"`
	Stars         int       `json:"star_count"`
	LastActivity  time.Time `json:"updated_on"`
	WebURL        string    `json:"web_url"`
}

func getGitLabDir(ctx context.Context, client *http.Client, match map[string]string, savedEtag string) (*Directory, error) {
	c := &httpClient{client: client}

	project, err := gitlabProject(ctx, c, match)
	if err != nil {
		return nil, err
	}
	match["ref"] = project.DefaultBranch
	match["project_id"] = strconv.Itoa(project.ID)
	match["dir"] = strings.TrimPrefix(match["dir"], "/")

	type glCommit struct {
		ID            string    `json:"id"`
		CommittedDate time.Time `json:"committed_date"`
	}

	var commits []*glCommit
	url := expand("https://gitlab.com/api/v4/projects/{project_id}/repository/commits?path={0}&per_page=1", match, strings.TrimPrefix(match["dir"], "/"))
	if _, err := c.getJSON(ctx, url, &commits); err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, NotFoundError{Message: "package directory changed or removed"}
	}

	status := Active
	lastCommitted := commits[0].CommittedDate
	if lastCommitted.Add(ExpiresAfter).Before(time.Now()) {
		status = NoRecentCommits
	}

	lastCommitOid := commits[0].ID
	newEtag := "git-" + lastCommitOid
	if newEtag == savedEtag {
		return nil, NotModifiedError{
			Since:  lastCommitted,
			Status: status,
		}
	}

	var contents []*struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Path string `json:"path"`
	}

	var files []*File
	var dataURLs []string
	var subDirs []string

	for i := 1; true; i++ {
		resp, err := c.getJSON(ctx,
			expand("https://gitlab.com/api/v4/projects/{project_id}/repository/tree?path={dir}&per_page=100&page=", match, strconv.Itoa(i)),
			&contents)
		if err != nil {
			return nil, err
		}

		for _, treeEntry := range contents {
			switch treeEntry.Type {
			case "blob":
				_, name := path.Split(treeEntry.Path)
				if isDocFile(name) {
					files = append(files, &File{Name: name, BrowseURL: expand("https://gitlab.com/{namespace}/{project}/blob/{ref}/{0}", match, treeEntry.Path)})
					dataURLs = append(dataURLs, expand("https://gitlab.com/api/v4/projects/{project_id}/repository/blobs/{0}/raw", match, treeEntry.ID))
				}
			case "tree":
				subDirs = append(subDirs, treeEntry.Path)
			}
		}

		if len(resp.Header["X-Total-Pages"]) == 0 {
			break
		}
		if pages, err := strconv.Atoi(resp.Header["X-Total-Pages"][0]); err != nil || pages <= i {
			break
		}
	}

	if err := c.getFiles(ctx, dataURLs, files); err != nil {
		return nil, err
	}

	browseURL := project.WebURL
	if match["dir"] != "" {
		browseURL = expand("https://gitlab.com/{namespace}/{project}/tree/{ref}/{dir}", match)
	}

	return &Directory{
		BrowseURL:      browseURL,
		Etag:           newEtag,
		Files:          files,
		Subdirectories: subDirs,
		LineFmt:        "%s#L%d",
		ProjectName:    project.Name,
		ProjectRoot:    expand("gitlab.com/{namespace}/{project}", match),
		ProjectURL:     project.WebURL,
		VCS:            "git",
		Status:         status,
		Stars:          project.Stars,
	}, nil
}

func getGitLabProject(ctx context.Context, c *http.Client, match map[string]string) (*Project, error) {
	pr, err := getGitLabProject(ctx, c, match)
	if err != nil {
		return nil, err
	}

	return &Project{Description: pr.Description}, nil
}

func gitlabProject(ctx context.Context, c *httpClient, match map[string]string) (*glProject, error) {
	var project glProject

	// GitLab API accepts a numerical ID, or a specially encoded string. The ID is unknown
	// here, so we use the backup method
	reqPath := match["namespace"] + "%2f" + match["project"]
	if _, err := c.getJSON(ctx, fmt.Sprintf("https://gitlab.com/api/v4/projects/%v", reqPath), &project); err != nil {
		return nil, err
	}

	return &project, nil
}
