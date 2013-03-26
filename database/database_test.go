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

package database_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/garyburd/gopkgdoc/database"
	"github.com/garyburd/gopkgdoc/doc"
	"github.com/garyburd/redigo/redis"
)

func newDB(t *testing.T) *database.Database {
	p := redis.NewPool(func() (redis.Conn, error) {
		c, err := redis.DialTimeout("tcp", ":6379", 0, 1*time.Second, 1*time.Second)
		if err != nil {
			return nil, err
		}
		_, err = c.Do("SELECT", "9")
		if err != nil {
			c.Close()
			return nil, err
		}
		return c, nil
	}, 1)

	c := p.Get()
	defer c.Close()
	n, err := redis.Int(c.Do("DBSIZE"))
	if n != 0 || err != nil {
		t.Fatalf("DBSIZE returned %d, %v", n, err)
	}
	return &database.Database{Pool: p}
}

func closeDB(db *database.Database) {
	c := db.Pool.Get()
	c.Do("FLUSHDB")
	c.Close()
}

func TestPutGet(t *testing.T) {
	var updated = time.Unix(1221681866, 0).UTC()

	db := newDB(t)
	defer closeDB(db)
	pdoc := &doc.Package{
		ImportPath:  "github.com/user/repo/foo/bar",
		Name:        "bar",
		Synopsis:    "hello",
		ProjectRoot: "github.com/user/repo",
		ProjectName: "foo",
		Updated:     updated,
		Imports:     []string{"C", "errors", "github.com/user/repo/foo/bar"}, // self import for testing convenience.
	}
	if err := db.Put(pdoc); err != nil {
		t.Errorf("db.Put() returned error %v", err)
	}
	if err := db.Put(pdoc); err != nil {
		t.Errorf("second db.Put() returned error %v", err)
	}

	actualPdoc, actualSubdirs, actualLastCrawl, err := db.Get("github.com/user/repo/foo/bar")
	if err != nil {
		t.Fatalf("db.Get(.../foo/bar) returned %v", err)
	}
	if len(actualSubdirs) != 0 {
		t.Errorf("db.Get(.../foo/bar) returned subdirs %v, want none", actualSubdirs)
	}
	if !reflect.DeepEqual(actualPdoc, pdoc) {
		t.Errorf("db.Get(.../foo/bar) returned doc %v, want %v", actualPdoc, pdoc)
	}
	if !updated.Equal(actualLastCrawl) {
		t.Errorf("db.get(.../foo/bar) returned crawl %v, want %v", actualLastCrawl, updated)
	}

	actualPdoc, _, _, err = db.Get("-")
	if err != nil {
		t.Fatalf("db.Get(-) returned %v", err)
	}
	if !reflect.DeepEqual(actualPdoc, pdoc) {
		t.Errorf("db.Get(-) returned doc %v, want %v", actualPdoc, pdoc)
	}

	actualPdoc, actualSubdirs, _, err = db.Get("github.com/user/repo/foo")
	if err != nil {
		t.Fatalf("db.Get(.../foo) returned %v", err)
	}
	if actualPdoc != nil {
		t.Errorf("db.Get(.../foo) returned doc %v, want %v", actualPdoc, nil)
	}
	expectedSubdirs := []database.Package{{Path: "github.com/user/repo/foo/bar", Synopsis: "hello"}}
	if !reflect.DeepEqual(actualSubdirs, expectedSubdirs) {
		t.Errorf("db.Get(.../foo) returned subdirs %v, want %v", actualSubdirs, expectedSubdirs)
	}
	actualImporters, err := db.Importers("github.com/user/repo/foo/bar")
	if err != nil {
		t.Fatalf("db.Importers() retunred error %v", err)
	}
	expectedImporters := []database.Package{{"github.com/user/repo/foo/bar", "hello"}}
	if !reflect.DeepEqual(actualImporters, expectedImporters) {
		t.Errorf("db.Importers() = %v, want %v", actualImporters, expectedImporters)
	}
	actualImports, err := db.Packages(pdoc.Imports)
	if err != nil {
		t.Fatalf("db.Imports() retunred error %v", err)
	}
	for i := range actualImports {
		if actualImports[i].Path == "C" {
			actualImports[i].Synopsis = ""
		}
	}
	expectedImports := []database.Package{{"C", ""}, {"errors", ""}, {"github.com/user/repo/foo/bar", "hello"}}
	if !reflect.DeepEqual(actualImports, expectedImports) {
		t.Errorf("db.Imports() = %v, want %v", actualImports, expectedImports)
	}
	importerCount, _ := db.ImporterCount("github.com/user/repo/foo/bar")
	if importerCount != 1 {
		t.Errorf("db.ImporterCount() = %d, want %d", importerCount, 1)
	}
	if err := db.Delete("github.com/user/repo/foo/bar"); err != nil {
		t.Errorf("db.Delete() returned error %v", err)
	}

	db.Query("bar")

	if err := db.Put(pdoc); err != nil {
		t.Errorf("db.Put() returned error %v", err)
	}

	if err := db.Block("github.com/user/repo"); err != nil {
		t.Errorf("db.Block() returned error %v", err)
	}

	blocked, err := db.IsBlocked("github.com/user/repo/foo/bar")
	if !blocked || err != nil {
		t.Errorf("db.IsBlocked(github.com/user/repo/foo/bar) returned %v, %v, want true, nil", blocked, err)
	}

	blocked, err = db.IsBlocked("github.com/foo/bar")
	if blocked || err != nil {
		t.Errorf("db.IsBlocked(github.com/foo/bar) returned %v, %v, want false, nil", blocked, err)
	}

	c := db.Pool.Get()
	defer c.Close()
	c.Send("DEL", "maxQueryId")
	c.Send("DEL", "maxPackageId")
	c.Send("DEL", "block")
	if n, err := c.Do("DBSIZE"); n != int64(0) || err != nil {
		t.Errorf("c.Do(DBSIZE) = %d, %v, want 0, nil", n, err)
	}
}
