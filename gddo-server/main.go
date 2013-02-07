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

// Command gddo-server is the GoPkgDoc server.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/gopkgdoc/database"
	"github.com/garyburd/gopkgdoc/doc"
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
		needsCrawl = time.Now().Add(-24 * time.Hour).After(lastCrawl)
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
				err = &web.Error{Status: web.StatusNotFound}
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
	if p := path.Clean(req.URL.Path); p != req.URL.Path {
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

	if pdoc == nil {
		if len(pkgs) == 0 {
			return &web.Error{Status: web.StatusNotFound}
		}
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

	switch req.Form.Get("view") {
	case "imports":
		if pdoc.Name == "" {
			return &web.Error{Status: web.StatusNotFound}
		}
		pkgs, err = db.Packages(pdoc.Imports)
		if err != nil {
			return err
		}
		return executeTemplate(resp, "imports.html", web.StatusOK, map[string]interface{}{
			"pkgs": pkgs,
			"pdoc": pdoc,
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
		return executeTemplate(resp, "home"+templateExt(req), web.StatusOK,
			map[string]interface{}{"Popular": getPopularPackages()})
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

func logError(req *web.Request, err error, r interface{}) {
	if err != nil {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "Error serving %s: %v\n", req.URL, err)
		if r != nil {
			fmt.Fprintln(&buf, r)
			buf.Write(debug.Stack())
		}
		log.Print(buf.String())
	}
}

func handleError(resp web.Response, req *web.Request, status int, err error, r interface{}) {
	logError(req, err, r)
	switch status {
	case 0:
		// nothing to do
	case web.StatusNotFound:
		executeTemplate(resp, "notfound"+templateExt(req), status, nil)
	default:
		s := web.StatusText(status)
		if err == errUpdateTimeout {
			s = "Timeout getting package files from the version control system."
		} else if e, ok := err.(*doc.RemoteError); ok {
			s = "Error getting package files from " + e.Host + "."
		}
		w := resp.Start(web.StatusInternalServerError, web.Header{web.HeaderContentType: {"text/plan; charset=uft-8"}})
		io.WriteString(w, s)
	}
}

var (
	presMu        sync.Mutex
	presentations = map[string]*doc.Presentation{}
)

func servePresentation(resp web.Response, req *web.Request) error {
	if p := path.Clean(req.URL.Path); p != req.URL.Path {
		return web.Redirect(resp, req, p, 301, nil)
	}
	p := req.RouteVars["path"]
	presMu.Lock()
	pres := presentations[p]
	presMu.Unlock()

	if pres == nil || time.Since(pres.Updated) > 30*time.Minute {
		var err error
		log.Println("Fetch presentation ", p)
		pres, err = doc.GetPresentation(httpClient, p)
		if err != nil {
			return err
		}
		presMu.Lock()
		presentations[p] = pres
		presMu.Unlock()
	}

	return executeTemplate(resp, path.Ext(p)[1:]+".html", 200, pres)
}

func defaultBase(path string) string {
	p, err := build.Default.Import(path, "", build.FindOnly)
	if err != nil {
		return "."
	}
	return p.Dir
}

var (
	db              *database.Database
	robot           = flag.Bool("robot", false, "Robot mode")
	baseDir         = flag.String("base", defaultBase("github.com/garyburd/gopkgdoc/gddo-server"), "Base directory for templates and static files.")
	presentBaseDir  = flag.String("presentBase", defaultBase("code.google.com/p/go.talks/present"), "Base directory for templates and static files.")
	getTimeout      = flag.Duration("get_timeout", 8*time.Second, "Time to wait for package update from the VCS.")
	firstGetTimeout = flag.Duration("first_get_timeout", 5*time.Second, "Time to wait for first fetch of package from the VCS.")
	maxAge          = flag.Duration("max_age", 24*time.Hour, "Crawl package documents older than this age.")
	httpAddr        = flag.String("http", ":8080", "Listen for HTTP connections on this address")
	crawlInterval   = flag.Duration("crawl_interval", 0, "Package crawler sleeps for this duration between package updates. Zero disables crawl.")
	popularInterval = flag.Duration("popular_interval", 0, "Google Analytics fetcher sleeps for this duration between updates. Zero disables updates.")
	secretsPath     = flag.String("secrets", "secrets.json", "Path to file containing application ids and credentials for other services.")
	secrets         struct {
		GithubId              string
		GithubSecret          string
		GAAccount             string
		ServiceAccountSecrets struct {
			Web struct {
				ClientEmail string `json:"client_email"`
				TokenURI    string `json:"token_uri"`
			}
		}
		ServiceAccountPEM      []string
		serviceAccountPEMBytes []byte
	}
)

func readSecrets() error {
	b, err := ioutil.ReadFile(*secretsPath)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &secrets); err != nil {
		return err
	}
	if secrets.GithubId != "" {
		doc.SetGithubCredentials(secrets.GithubId, secrets.GithubSecret)
	} else {
		log.Printf("Github credentials not set in %q.", *secretsPath)
	}
	if secrets.GAAccount == "" {
		log.Printf("Google Analytics account not set in %q", *secretsPath)
	}
	secrets.serviceAccountPEMBytes = []byte(strings.Join(secrets.ServiceAccountPEM, "\n"))
	return nil
}

func main() {
	flag.Parse()
	log.Printf("Starting server, os.Args=%s", strings.Join(os.Args, " "))
	if err := readSecrets(); err != nil {
		log.Fatal(err)
	}

	if err := parseHTMLTemplates([][]string{
		{"about.html", "common.html"},
		{"cmd.html", "common.html"},
		{"home.html", "common.html"},
		{"importers.html", "common.html"},
		{"imports.html", "common.html"},
		{"index.html", "common.html"},
		{"notfound.html", "common.html"},
		{"pkg.html", "common.html"},
		{"results.html", "common.html"},
		{"std.html", "common.html"},
	}); err != nil {
		log.Fatal(err)
	}

	if err := parseTextTemplates([][]string{
		{"cmd.txt", "common.txt"},
		{"home.txt", "common.txt"},
		{"notfound.txt", "common.txt"},
		{"pkg.txt", "common.txt"},
		{"results.txt", "common.txt"},
	}); err != nil {
		log.Fatal(err)
	}

	if err := parsePresentTemplates([][]string{
		{"article.html", "presentCommon.html"},
		{"slide.html", "presentCommon.html"},
	}); err != nil {
		log.Fatal(err)
	}

	var err error
	db, err = database.New()
	if err != nil {
		log.Fatal(err)
	}

	if *popularInterval > 0 {
		go updatePopularPackages(*popularInterval)
	}

	if *crawlInterval > 0 {
		go crawl(*crawlInterval)
	}

	sfo := &web.ServeFileOptions{
		Header: web.Header{
			web.HeaderCacheControl: {"public, max-age=3600"},
		},
	}

	r := web.NewRouter()
	r.Add("/").GetFunc(serveHome)
	r.Add("/-/about").GetFunc(serveAbout)
	r.Add("/-/go").GetFunc(serveGoIndex)
	r.Add("/-/index").GetFunc(serveIndex)
	r.Add("/-/refresh").PostFunc(serveRefresh)
	r.Add("/-/static/<path:.*>").Get(web.DirectoryHandler(filepath.Join(*baseDir, "static"), sfo))
	r.Add("/-/talk/<path:.+>").GetFunc(servePresentation)
	r.Add("/robots.txt").Get(web.FileHandler(filepath.Join(*baseDir, "static", "robots.txt"), nil))
	r.Add("/humans.txt").Get(web.FileHandler(filepath.Join(*baseDir, "static", "humans.txt"), nil))
	r.Add("/favicon.ico").Get(web.FileHandler(filepath.Join(*baseDir, "static", "favicon.ico"), nil))
	r.Add("/google3d2f3cd4cc2bb44b.html").Get(web.FileHandler(filepath.Join(*baseDir, "static", "google3d2f3cd4cc2bb44b.html"), nil))
	r.Add("/about").Get(web.RedirectHandler("/-/about", 301))
	r.Add("/a/index").Get(web.RedirectHandler("/-/index", 301))
	r.Add("/C").Get(web.RedirectHandler("http://golang.org/doc/articles/c_go_cgo.html", 301))
	r.Add("/<path:.+>").GetFunc(servePackage)
	h := web.ErrorHandler(handleError, web.FormAndCookieHandler(1000, false, r))

	listener, err := net.Listen("tcp", *httpAddr)
	if err != nil {
		log.Fatal("Listen", err)
		return
	}
	defer listener.Close()
	s := &server.Server{Listener: listener, Handler: h} // add logger
	err = s.Serve()
	if err != nil {
		log.Fatal("Server", err)
	}
}
