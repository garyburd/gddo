// Copyright 2015 The Go Authors. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file or at
// https://developers.google.com/open-source/licenses/bsd.

package doc

import (
	"go/doc"
	"go/parser"
	"go/token"
	"testing"
)

var annotationTests = []struct {
	src              string
	desc             string
	annotationsCount int
}{
	{`
		package e

		import (
			"fmt"
		)
		const C = 1.0

		func ExampleTest() {
			fmt.Println("%d", C);
		}
	`, "Const", 1},
	{`
		package e

		type T struct {
		}

		func ExampleTest() {
			a := &e.T{}
		}
	`, "Type", 1},
	{`
		package e

		type T struct {
		}

		func ExampleTest() {
			var a e.T
		}
	`, "Var", 1},
}

func TestAnnotation(t *testing.T) {
	for _, tt := range annotationTests {
		code := printExample(tt.src)

		if len(code.Annotations) != tt.annotationsCount {
			t.Errorf("Error in test '%s': expected %d annotation, but found %d", tt.desc, tt.annotationsCount, len(code.Annotations))
		}
	}
}

func printExample(src string) Code {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "src.go", src, 0)
	if err != nil {
		panic(err)
	}

	examples := doc.Examples(f)
	builder := &builder{}
	builder.fset = fset
	builder.pkgName = "e"
	code, _ := builder.printExample(examples[0])
	return code
}
