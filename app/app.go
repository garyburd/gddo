// Copyright 2011 Gary Burd
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

package app

import (
	"appengine"
	"appengine/datastore"
	"go/doc"
	"http"
	"io"
	"json"
	"os"
	"template"
	"time"
)

func commentFmt(w io.Writer, format string, x ...interface{}) {
	doc.ToHTML(w, []byte(x[0].(string)), nil)
}

var homeTemplate = template.MustParseFile("template/home.html", template.FormatterMap{
	"": template.HTMLFormatter,
})

var pkgTemplate = template.MustParseFile("template/pkg.html", template.FormatterMap{
	"":        template.HTMLFormatter,
	"comment": commentFmt,
})

func internalError(w http.ResponseWriter, c appengine.Context, err os.Error) {
	c.Errorf("Error %s", err.String())
	http.Error(w, "Internal Error", http.StatusInternalServerError)
}

func servePkg(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	key := datastore.NewKey("PackageDoc", r.URL.Path[len("/pkg/"):], 0, nil)
	var doc PackageDoc
	err := datastore.Get(appengine.NewContext(r), key, &doc)
	if err == datastore.ErrNoSuchEntity {
		http.NotFound(w, r)
		return
	} else if err != nil {
		internalError(w, c, err)
		return
	}

	var m map[string]interface{}
	if err := json.Unmarshal(doc.Data, &m); err != nil {
		c.Errorf("error unmarshalling json", err)
	}

	m["importPath"] = doc.ImportPath
	m["packageName"] = doc.PackageName
	m["projectURL"] = doc.ProjectURL
	m["projectName"] = doc.ProjectName
	m["updated"] = time.SecondsToLocalTime(int64(doc.Updated) / 1e6).String()
	if err := pkgTemplate.Execute(w, m); err != nil {
		c.Errorf("error rendering pkg template:", err)
	}
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	c := appengine.NewContext(r)
	var imports []string
	if item, found := cacheGet(c, "/", &imports); !found {
		q := datastore.NewQuery("PackageDoc").KeysOnly()
		keys, err := q.GetAll(c, nil)
		if err != nil {
			internalError(w, c, err)
			return
		}
		for _, key := range keys {
			imports = append(imports, key.StringID())
		}
		cacheSet(c, item, 7200, imports)
	}
	if err := homeTemplate.Execute(w, imports); err != nil {
		c.Errorf("error rendering home template:", err)
	}
}

func init() {
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/pkg/", servePkg)
	http.HandleFunc("/hook/github", githubHook)
	http.HandleFunc("/admin/task/github", githubTask)
}
