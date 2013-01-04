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

// Command reindex re-indexes the GoPkgDoc document store by fetching and storing all documents.
package main

import (
	"flag"
	"log"

	"github.com/garyburd/gopkgdoc/database"
	"github.com/garyburd/gopkgdoc/doc"
)

func main() {
	flag.Parse()
	db, err := database.New()
	if err != nil {
		log.Fatal(err)
	}
	var n int
	err = db.Do(func(pdoc *doc.Package, pkgs []database.Package) error {
		n += 1
		return db.Put(pdoc)
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Updated %d documents.", n)
}
