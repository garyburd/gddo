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

// Redis keys and types:
//
// id:<path> string: id for given import path
// pkg:<id> hash
//      terms: space separated search terms
//      path: import path
//      synopsis: synopsis
//      glob: snappy compressed gob encoded doc.Package
//      rank: document search rank
// index:<term> set: package ids for given search term
// index:import:<path> set: packages with import path
// index:project:<root> set: packges in project with root
// crawl zset: package id, unix type of last crawl

// Package database manages storage for GoPkgDoc.
package database

import (
	"bytes"
	"encoding/gob"
	"flag"
	"log"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"code.google.com/p/snappy-go/snappy"
	"github.com/garyburd/gopkgdoc/doc"
	"github.com/garyburd/redigo/redis"
)

type Database struct {
	Pool interface {
		Get() redis.Conn
	}
}

type Package struct {
	Path     string
	Synopsis string
}

type byPath []Package

func (p byPath) Len() int           { return len(p) }
func (p byPath) Less(i, j int) bool { return p[i].Path < p[j].Path }
func (p byPath) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

var (
	redisServer      = flag.String("db-server", "redis://127.0.0.0:6379", "Redis URI of Redis server.")
	redisIdleTimeout = flag.Duration("db-idle-timeout", 250*time.Second, "Close Redis connections after remaining idle for this duration.")
	redisLog         = flag.Bool("db-log", false, "Log database commands")
)

func dialDb() (redis.Conn, error) {
	redisUri, err := url.Parse(*redisServer)
	if err != nil {
		return nil, err
	}
	c, err := redis.Dial("tcp", redisUri.Host)
	if err != nil {
		return nil, err
	}
	if *redisLog {
		l := log.New(os.Stderr, "", log.LstdFlags)
		c = redis.NewLoggingConn(c, l, "")
	}

	if redisUri.User != nil {
		password, hasPassword := redisUri.User.Password()
		if hasPassword {
			_, err = redis.String(c.Do("AUTH", password))
			if err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

// New creates a database configured from command line flags.
func New() (*Database, error) {
	pool := &redis.Pool{
		Dial:        dialDb,
		MaxIdle:     10,
		IdleTimeout: *redisIdleTimeout,
	}

	if c := pool.Get(); c.Err() != nil {
		return nil, c.Err()
	} else {
		c.Close()
	}

	return &Database{Pool: pool}, nil
}

// Exists returns true if package with import path exists in the database.
func (db *Database) Exists(path string) (bool, error) {
	c := db.Pool.Get()
	defer c.Close()
	return redis.Bool(c.Do("EXISTS", "id:"+path))
}

var putScript = redis.NewScript(0, `
    local path = ARGV[1]
    local synopsis = ARGV[2]
    local rank = ARGV[3]
    local gob = ARGV[4]
    local terms = ARGV[5]
    local crawl = ARGV[6]

    local id = redis.call('GET', 'id:' .. path)
    if not id then
        id = redis.call('INCR', 'maxPackageId')
        redis.call('SET', 'id:' .. path, id)
    end

    local update = {}
    for term in string.gmatch(redis.call('HGET', 'pkg:' .. id, 'terms') or '', '([^ ]+)') do
        update[term] = 1
    end

    for term in string.gmatch(terms, '([^ ]+)') do
        update[term] = (update[term] or 0) + 2
    end

    for term, x in pairs(update) do
        if x == 1 then
            redis.call('SREM', 'index:' .. term, id)
        elseif x == 2 then 
            redis.call('SADD', 'index:' .. term, id)
        end
    end

    local c = redis.call('ZSCORE', 'crawl', id)
    if not c or tonumber(c) < tonumber(crawl) then
        redis.call('ZADD', 'crawl', crawl, id)
    end

    return redis.call('HMSET', 'pkg:' .. id, 'path', path, 'synopsis', synopsis, 'rank', rank, 'gob', gob, 'terms', terms)
`)

// Put adds the package documentation to the database.
func (db *Database) Put(pdoc *doc.Package) error {
	c := db.Pool.Get()
	defer c.Close()

	projectRoot := pdoc.ProjectRoot
	if projectRoot == "" {
		projectRoot = "go"
	}

	rank := documentRank(pdoc)
	terms := documentTerms(pdoc, rank)

	var gobBuf bytes.Buffer
	if err := gob.NewEncoder(&gobBuf).Encode(pdoc); err != nil {
		return err
	}

	// Truncate large documents.
	if gobBuf.Len() > 700000 {
		pdocNew := *pdoc
		pdoc = &pdocNew
		pdoc.Truncated = true
		pdoc.Vars = nil
		pdoc.Funcs = nil
		pdoc.Types = nil
		pdoc.Consts = nil
		pdoc.Examples = nil
		gobBuf.Reset()
		if err := gob.NewEncoder(&gobBuf).Encode(pdoc); err != nil {
			return err
		}
	}

	gobBytes, err := snappy.Encode(nil, gobBuf.Bytes())
	if err != nil {
		return err
	}

	_, err = putScript.Do(c, pdoc.ImportPath, pdoc.Synopsis, rank, gobBytes, strings.Join(terms, " "), pdoc.Updated.Unix())
	return err
}

var touchScript = redis.NewScript(0, `
    local path = ARGV[1]
    local crawl = ARGV[2]

    local id = redis.call('GET', 'id:' .. path)
    if id then
        redis.call('ZADD', 'crawl', crawl, id)
    end
`)

func (db *Database) TouchLastCrawl(path string) error {
	c := db.Pool.Get()
	defer c.Close()
	_, err := touchScript.Do(c, path, time.Now().Unix())
	return err
}

// getDocScript gets the package documentation and last crawl time for the
// specified path. If path is "-", then the oldest crawled document is
// returned.
var getDocScript = redis.NewScript(0, `
    local path = ARGV[1]

    local id
    if path == '-' then
        local r = redis.call('ZRANGE', 'crawl', 0, 0)
        if not r then
            return false
        end
        id = r[1]
    else
        id = redis.call('GET', 'id:' .. path)
        if not id then
            return false
        end
    end

    local gob = redis.call('HGET', 'pkg:' .. id, 'gob')
    if not gob then
        return false
    end

    local crawl = redis.call('ZSCORE', 'crawl', id)
    if not crawl then 
        crawl = 0
    end
    
    return {gob, crawl}
`)

func (db *Database) getDoc(c redis.Conn, path string) (*doc.Package, time.Time, error) {
	r, err := redis.Values(getDocScript.Do(c, path))
	if err == redis.ErrNil {
		return nil, time.Time{}, nil
	} else if err != nil {
		return nil, time.Time{}, err
	}

	var p []byte
	var t int64

	if _, err := redis.Scan(r, &p, &t); err != nil {
		return nil, time.Time{}, err
	}

	p, err = snappy.Decode(nil, p)
	if err != nil {
		return nil, time.Time{}, err
	}

	var pdoc doc.Package
	if err := gob.NewDecoder(bytes.NewReader(p)).Decode(&pdoc); err != nil {
		return nil, time.Time{}, err
	}

	lastCrawl := pdoc.Updated
	if t != 0 {
		lastCrawl = time.Unix(t, 0).UTC()
	}

	return &pdoc, lastCrawl, err
}

var getSubdirsScript = redis.NewScript(0, `
    local reply
    for i = 1,#ARGV do
        reply = redis.call('SORT', 'index:project:' .. ARGV[i], 'ALPHA', 'BY', 'pkg:*->path', 'GET', 'pkg:*->path', 'GET', 'pkg:*->synopsis')
        if #reply > 0 then
            break
        end
    end
    return reply
`)

func (db *Database) getSubdirs(c redis.Conn, path string, pdoc *doc.Package) ([]Package, error) {
	var reply interface{}
	var err error

	switch {
	case isStandardPackage(path):
		reply, err = getSubdirsScript.Do(c, "go")
	case pdoc != nil:
		reply, err = getSubdirsScript.Do(c, pdoc.ProjectRoot)
	default:
		var roots []interface{}
		projectRoot := path
		for i := 0; i < 5; i++ {
			roots = append(roots, projectRoot)
			if j := strings.LastIndex(projectRoot, "/"); j < 0 {
				break
			} else {
				projectRoot = projectRoot[:j]
			}
		}
		reply, err = getSubdirsScript.Do(c, roots...)
	}

	values, err := redis.Values(reply, err)
	if err != nil {
		return nil, err
	}

	var subdirs []Package
	prefix := path + "/"

	for len(values) > 0 {
		var pkg Package
		values, err = redis.Scan(values, &pkg.Path, &pkg.Synopsis)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(pkg.Path, prefix) {
			subdirs = append(subdirs, pkg)
		}
	}

	return subdirs, err
}

// Get gets the package documenation and sub-directories for the the given
// import path.
func (db *Database) Get(path string) (*doc.Package, []Package, time.Time, error) {
	c := db.Pool.Get()
	defer c.Close()

	pdoc, lastCrawl, err := db.getDoc(c, path)
	if err != nil {
		return nil, nil, time.Time{}, err
	}

	if pdoc != nil {
		// fixup for speclal "-" path.
		path = pdoc.ImportPath
	}

	subdirs, err := db.getSubdirs(c, path, pdoc)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	return pdoc, subdirs, lastCrawl, nil
}

var deleteScript = redis.NewScript(0, `
    local path = ARGV[1]

    local id = redis.call('GET', 'id:' .. path)
    if not id then
        return false
    end

    for term in string.gmatch(redis.call('HGET', 'pkg:' .. id, 'terms') or '', '([^ ]+)') do
        redis.call('SREM', 'index:' .. term, id)
    end

    redis.call('ZREM', 'crawl', id)
    redis.call('DEL', 'pkg:' .. id)
    return redis.call('DEL', 'id:' .. path)
`)

// Delete deletes the documenation for the given import path.
func (db *Database) Delete(path string) error {
	c := db.Pool.Get()
	defer c.Close()
	_, err := deleteScript.Do(c, path)
	return err
}

func packages(reply interface{}) ([]Package, error) {
	values, err := redis.Values(reply, nil)
	if err != nil {
		return nil, err
	}
	result := make([]Package, 0, len(values)/2)
	for len(values) > 0 {
		var pkg Package
		values, err = redis.Scan(values, &pkg.Path, &pkg.Synopsis)
		if err != nil {
			return nil, err
		}
		if pkg.Path == "C" {
			pkg.Synopsis = "Package C is a \"pseudo-package\" used to access the C namespace from a cgo source file."
		}
		result = append(result, pkg)
	}
	return result, nil
}

func (db *Database) GoIndex() ([]Package, error) {
	c := db.Pool.Get()
	defer c.Close()
	reply, err := c.Do("SORT", "index:project:go", "ALPHA", "BY", "pkg:*->path", "GET", "pkg:*->path", "GET", "pkg:*->synopsis")
	if err != nil {
		return nil, err
	}
	return packages(reply)
}

func (db *Database) Index() ([]Package, error) {
	c := db.Pool.Get()
	defer c.Close()
	reply, err := c.Do("SORT", "index:all:", "ALPHA", "BY", "pkg:*->path", "GET", "pkg:*->path", "GET", "pkg:*->synopsis")
	if err != nil {
		return nil, err
	}
	return packages(reply)
}

func (db *Database) Project(projectRoot string) ([]Package, error) {
	c := db.Pool.Get()
	defer c.Close()
	if projectRoot == "" {
		projectRoot = "go"
	}
	reply, err := c.Do("SORT", "index:project:"+projectRoot, "ALPHA", "BY", "pkg:*->path", "GET", "pkg:*->path", "GET", "pkg:*->synopsis")
	if err != nil {
		return nil, err
	}
	return packages(reply)
}

var importsScript = redis.NewScript(0, `
    local result = {}
    for i = 1,#ARGV do
        local path = ARGV[i]
        local synopsis = ''
        local id = redis.call('GET', 'id:' .. path)
        if id then
            synopsis = redis.call('HGET', 'pkg:' .. id, 'synopsis')
        end
        result[#result+1] = path
        result[#result+1] = synopsis
    end
    return result
`)

func (db *Database) Imports(pdoc *doc.Package) ([]Package, error) {
	var args []interface{}
	for _, p := range pdoc.Imports {
		args = append(args, p)
	}
	c := db.Pool.Get()
	defer c.Close()
	reply, err := importsScript.Do(c, args...)
	if err != nil {
		return nil, err
	}
	pkgs, err := packages(reply)
	sort.Sort(byPath(pkgs))
	return pkgs, err
}

func (db *Database) ImporterCount(path string) (int, error) {
	c := db.Pool.Get()
	defer c.Close()
	return redis.Int(c.Do("SCARD", "index:import:"+path))
}

func (db *Database) Importers(path string) ([]Package, error) {
	c := db.Pool.Get()
	defer c.Close()
	reply, err := c.Do("SORT", "index:import:"+path, "ALPHA", "BY", "pkg:*->path", "GET", "pkg:*->path", "GET", "pkg:*->synopsis")
	if err != nil {
		return nil, err
	}
	return packages(reply)
}

func (db *Database) Query(q string) ([]Package, error) {
	terms := parseQuery(q)
	if len(terms) == 0 {
		return nil, nil
	}
	c := db.Pool.Get()
	defer c.Close()
	n, err := redis.Int(c.Do("INCR", "maxQueryId"))
	if err != nil {
		return nil, err
	}
	id := "tmp:query-" + strconv.Itoa(n)

	args := []interface{}{id}
	for _, term := range terms {
		args = append(args, "index:"+term)
	}
	c.Send("SINTERSTORE", args...)
	c.Send("SORT", id, "DESC", "BY", "pkg:*->rank", "GET", "pkg:*->path", "GET", "pkg:*->synopsis")
	c.Send("DEL", id)
	values, err := redis.Values(c.Do(""))
	if err != nil {
		return nil, err
	}
	pkgs, err := packages(values[1])

	// Move exact match on standard package to the top of the list.
	for i, pkg := range pkgs {
		if !isStandardPackage(pkg.Path) {
			break
		}
		if strings.HasSuffix(pkg.Path, q) {
			pkgs[0], pkgs[i] = pkgs[i], pkgs[0]
			break
		}
	}
	return pkgs, err
}

// Do executes function f for each document in the database.
func (db *Database) Do(f func(*doc.Package, []Package) error) error {
	c := db.Pool.Get()
	defer c.Close()
	keys, err := redis.Values(c.Do("KEYS", "pkg:*"))
	if err != nil {
		return err
	}
	for _, key := range keys {
		p, err := redis.Bytes(c.Do("HGET", key, "gob"))
		if err == redis.ErrNil {
			continue
		}
		if err != nil {
			return err
		}
		p, err = snappy.Decode(nil, p)
		if err != nil {
			return err
		}
		var pdoc doc.Package
		if err := gob.NewDecoder(bytes.NewReader(p)).Decode(&pdoc); err != nil {
			return err
		}
		pkgs, err := db.getSubdirs(c, pdoc.ImportPath, &pdoc)
		if err != nil {
			return err
		}
		if err := f(&pdoc, pkgs); err != nil {
			return err
		}
	}
	return nil
}

var importGraphScript = redis.NewScript(0, `
    local path = ARGV[1]

    local id = redis.call('GET', 'id:' .. path)
    if not id then
        return false
    end

    return redis.call('HMGET', 'pkg:' .. id, 'synopsis', 'terms')
`)

func (db *Database) ImportGraph(pdoc *doc.Package) ([]Package, [][2]int, error) {

	// This breadth-first traversal of the package's dependencies uses the
	// Redis pipeline as queue. Links to packages with invalid import paths are
	// only included for the root package.

	c := db.Pool.Get()
	defer c.Close()
	if err := importGraphScript.Load(c); err != nil {
		return nil, nil, err
	}

	nodes := []Package{{Path: pdoc.ImportPath, Synopsis: pdoc.Synopsis}}
	edges := [][2]int{}
	index := map[string]int{pdoc.ImportPath: 0}

	for _, path := range pdoc.Imports {
		j := len(nodes)
		index[path] = j
		edges = append(edges, [2]int{0, j})
		nodes = append(nodes, Package{Path: path})
		importGraphScript.Send(c, path)
	}

	for i := 1; i < len(nodes); i++ {
		c.Flush()
		r, err := redis.Values(c.Receive())
		if err == redis.ErrNil {
			continue
		} else if err != nil {
			return nil, nil, err
		}
		var synopsis, terms string
		if _, err := redis.Scan(r, &synopsis, &terms); err != nil {
			return nil, nil, err
		}
		nodes[i].Synopsis = synopsis
		for _, term := range strings.Fields(terms) {
			if strings.HasPrefix(term, "import:") {
				path := term[len("import:"):]
				j, ok := index[path]
				if !ok {
					j = len(nodes)
					index[path] = j
					nodes = append(nodes, Package{Path: path})
					importGraphScript.Send(c, path)
				}
				edges = append(edges, [2]int{i, j})
			}
		}
	}
	return nodes, edges, nil
}
