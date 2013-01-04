// Copyright 2012 Gary Burd
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

// Command server is the GoPkgDoc server.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/garyburd/gopkgdoc/database"
	"github.com/garyburd/gopkgdoc/doc"
	"github.com/garyburd/gopkgdoc/popularity"
	"github.com/garyburd/indigo/server"
	"github.com/garyburd/indigo/web"
)

var errUpdateTimeout = errors.New("refresh timeout")

const (
	humanRequest = iota
	robotRequest
	queryRequest
	refreshRequest
)

type crawlResult struct {
	pdoc *doc.Package
	err  error
}

// getDoc gets the package documentation from the database or from the version
// control system as needed.
func getDoc(path string, requestType int) (*doc.Package, []database.Package, error) {
	pdoc, pkgs, lastCrawl, err := db.Get(path)
	if err != nil {
		return nil, nil, err
	}

	needsCrawl := false
	switch requestType {
	case queryRequest:
		needsCrawl = lastCrawl.IsZero() && len(pkgs) == 0
	case humanRequest:
		needsCrawl = time.Now().Add(-time.Hour).After(lastCrawl)
	case robotRequest:
		needsCrawl = lastCrawl.IsZero() && len(pkgs) > 0
	}

	if needsCrawl {
		c := make(chan crawlResult, 1)
		go func() {
			pdoc, err := crawlDoc("web  ", path, pdoc, len(pkgs) > 0)
			c <- crawlResult{pdoc, err}
		}()
		var err error
		timeout := *getTimeout
		if pdoc == nil {
			timeout = *firstGetTimeout
		}
		select {
		case rr := <-c:
			if rr.err == nil {
				pdoc = rr.pdoc
			}
			err = rr.err
		case <-time.After(timeout):
			err = errUpdateTimeout
		}
		if err != nil {
			if pdoc != nil {
				log.Printf("Serving %q from database after error: %v", path, err)
				err = nil
			} else if err == errUpdateTimeout {
				// Handle timeout on packages never seeen before as not found.
				log.Printf("Serving %q as not found after timeout", path)
				err = doc.ErrPackageNotFound
			}
		}
	}
	return pdoc, pkgs, err
}

func templateExt(req *web.Request) string {
	if web.NegotiateContentType(req, []string{"text/html", "text/plain"}, "text/html") == "text/plain" {
		return ".txt"
	}
	return ".html"
}

var robotPat = regexp.MustCompile(`(:?\+https?://)|(?:\Wbot\W)`)

func isRobot(req *web.Request) bool {
	return *robot || robotPat.MatchString(req.Header.Get(web.HeaderUserAgent))
}

func servePackage(resp web.Response, req *web.Request) error {
	p := path.Clean(req.URL.Path)
	if p != req.URL.Path {
		return web.Redirect(resp, req, p, 301, nil)
	}

	requestType := humanRequest
	if isRobot(req) {
		requestType = robotRequest
	}

	path := req.RouteVars["path"]
	pdoc, pkgs, err := getDoc(path, requestType)
	if err != nil {
		return err
	}

	if (pdoc == nil || pdoc.Name == "") && len(pkgs) == 0 {
		return &web.Error{Status: web.StatusNotFound}
	}

	if pdoc == nil {
		pdocChild, _, _, err := db.Get(pkgs[0].Path)
		if err != nil {
			return err
		}
		pdoc = &doc.Package{
			ProjectName: pdocChild.ProjectName,
			ProjectRoot: pdocChild.ProjectRoot,
			ProjectURL:  pdocChild.ProjectURL,
			ImportPath:  path,
		}
	}

	if requestType != robotRequest {
		freq.Add(path)
		recent.Add(path)
	}

	switch req.Form.Get("view") {
	case "imports":
		if pdoc.Name == "" {
			return &web.Error{Status: web.StatusNotFound}
		}
		pkgs, err = db.Imports(pdoc)
		if err != nil {
			return err
		}
		return executeTemplate(resp, "imports.html", web.StatusOK, map[string]interface{}{
			"pkgs": pkgs,
			"pdoc": pdoc,
		})
	case "importGraph":
		if pdoc.Name == "" {
			return &web.Error{Status: web.StatusNotFound}
		}
		nodes, edges, err := db.ImportGraph(pdoc)
		if err != nil {
			return err
		}
		return executeTemplate(resp, "graph.html", web.StatusOK, map[string]interface{}{
			"nodes": nodes,
			"edges": edges,
			"pdoc":  pdoc,
		})
	case "importers":
		if pdoc.Name == "" {
			return &web.Error{Status: web.StatusNotFound}
		}
		pkgs, err = db.Importers(path)
		if err != nil {
			return err
		}
		return executeTemplate(resp, "importers.html", web.StatusOK, map[string]interface{}{
			"pkgs": pkgs,
			"pdoc": pdoc,
		})
	case "":
		importerCount, err := db.ImporterCount(path)
		if err != nil {
			return err
		}

		template := "pkg"
		if pdoc.IsCmd {
			template = "cmd"
		}
		template += templateExt(req)

		return executeTemplate(resp, template, 200, map[string]interface{}{
			"pkgs":          pkgs,
			"pdoc":          pdoc,
			"importerCount": importerCount,
		})
	}
	return &web.Error{Status: web.StatusNotFound}
}

func serveRefresh(resp web.Response, req *web.Request) error {
	path := req.Form.Get("path")
	_, pkgs, _, err := db.Get(path)
	if err != nil {
		return err
	}
	c := make(chan error, 1)
	go func() {
		_, err := crawlDoc("rfrsh", path, nil, len(pkgs) > 0)
		c <- err
	}()
	select {
	case err = <-c:
	case <-time.After(*getTimeout):
		err = errUpdateTimeout
	}
	if err != nil {
		return err
	}
	return web.Redirect(resp, req, "/"+path, 302, nil)
}

func serveGoIndex(resp web.Response, req *web.Request) error {
	pkgs, err := db.GoIndex()
	if err != nil {
		return err
	}
	return executeTemplate(resp, "std.html", 200, map[string]interface{}{
		"pkgs": pkgs,
	})
}

func serveIndex(resp web.Response, req *web.Request) error {
	pkgs, err := db.Index()
	if err != nil {
		return err
	}
	return executeTemplate(resp, "index.html", 200, map[string]interface{}{
		"pkgs": pkgs,
	})
}

func serveHome(resp web.Response, req *web.Request) error {

	q := strings.TrimSpace(req.Form.Get("q"))
	if q == "" {
		return executeTemplate(resp, "home"+templateExt(req), web.StatusOK, map[string]interface{}{ "freq": freq.Get(), "recent": recent.Get() })
	}

	if path, ok := isBrowseURL(q); ok {
		q = path
	}

	if doc.IsValidRemotePath(q) {
		pdoc, pkgs, err := getDoc(q, queryRequest)
		if err == nil && (pdoc != nil || len(pkgs) > 0) {
			return web.Redirect(resp, req, "/"+q, 302, nil)
		}
	}

	pkgs, err := db.Query(q)
	if err != nil {
		return err
	}

	return executeTemplate(resp, "results"+templateExt(req), 200, map[string]interface{}{"q": q, "pkgs": pkgs})
}

func serveAbout(resp web.Response, req *web.Request) error {
	return executeTemplate(resp, "about.html", 200, map[string]interface{}{"Host": req.URL.Host})
}

func handleError(resp web.Response, req *web.Request, status int, err error, r interface{}) {
	if err != nil {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "Error serving %s: %v\n", req.URL, err)
		if r != nil {
			fmt.Fprintln(&buf, r)
			buf.Write(debug.Stack())
		}
		log.Print(buf.String())
	}
	switch status {
	case 0:
		// nothing to do
	case web.StatusNotFound:
		executeTemplate(resp, "notfound"+templateExt(req), status, nil)
	default:
		s := web.StatusText(status)
		if err == errUpdateTimeout {
			s = "Timeout getting package files from the version control system."
		} else if e, ok := err.(doc.GetError); ok {
			s = "Error getting package files from " + e.Host + "."
		}
		w := resp.Start(web.StatusInternalServerError, web.Header{web.HeaderContentType: {"text/plan; charset=uft-8"}})
		io.WriteString(w, s)
	}
}

var (
	robot           = flag.Bool("robot", false, "Robot mode")
	templateDir     = flag.String("template", "template", "Template directory.")
	staticDir       = flag.String("static", "static", "Static file directory.")
	getTimeout      = flag.Duration("get_timeout", 8*time.Second, "Timeout for updating package documentation.")
	firstGetTimeout = flag.Duration("first_get_timeout", 5*time.Second, "Timeout for getting package documentation the first time.")
	maxAge          = flag.Duration("max_age", 24*time.Hour, "Crawl documents older than this age.")
	httpAddr        = flag.String("http", ":8080", "Listen for HTTP connections on this address")
	crawlInterval   = flag.Duration("crawl_interval", 30*time.Second, "Sleep for duration between document crawls.")
	db              *database.Database
)

var freq *popularity.Frequent
var recent *popularity.Recent

func main() {
	flag.Parse()
	log.Printf("Starting server, os.Args=%s", strings.Join(os.Args, " "))

	var err error
	templateSet, err = parseTemplates(*templateDir)
	if err != nil {
		log.Fatal(err)
	}

	db, err = database.New()
	if err != nil {
		log.Fatal(err)
	}

	freq = popularity.NewFrequent(10)
	recent = popularity.NewRecent(10)

	go crawl()

	sfo := &web.ServeFileOptions{
		Header: web.Header{
			web.HeaderCacheControl: {"public, max-age=3600"},
		},
	}

	r := web.ErrorHandler(handleError,
		web.FormAndCookieHandler(1000, false, web.NewRouter().
			AddGet("/", serveHome).
			AddGet("/-/about", serveAbout).
			AddGet("/-/go", serveGoIndex).
			AddGet("/-/index", serveIndex).
			AddPost("/-/refresh", serveRefresh).
			AddGet("/-/static/<path:.*>", web.DirectoryHandler(*staticDir, sfo)).
			AddGet("/robots.txt", web.FileHandler(filepath.Join(*staticDir, "robots.txt"), nil)).
			AddGet("/favicon.ico", web.FileHandler(filepath.Join(*staticDir, "favicon.ico"), nil)).
			AddGet("/google3d2f3cd4cc2bb44b.html", web.FileHandler(filepath.Join(*staticDir, "google3d2f3cd4cc2bb44b.html"), nil)).
			AddGet("/about", web.RedirectHandler("/-/about", 301)).
			AddGet("/a/index", web.RedirectHandler("/-/index", 301)).
			AddGet("/C", web.RedirectHandler("http://golang.org/doc/articles/c_go_cgo.html", 301)).
			AddGet("/<path:.*>", servePackage)))

	listener, err := net.Listen("tcp", *httpAddr)
	if err != nil {
		log.Fatal("Listen", err)
		return
	}
	defer listener.Close()
	s := &server.Server{Listener: listener, Handler: r} // add logger
	err = s.Serve()
	if err != nil {
		log.Fatal("Server", err)
	}
}
