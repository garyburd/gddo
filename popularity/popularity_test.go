// Copyright 2013, Jeff R. Allen <jra@nella.org>
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

package popularity

import (
	"testing"
)

type tester struct {
	expect  []string
	add     string
	process bool
}

var tests = []tester{
	{[]string{}, "a", true},
	{[]string{"a"}, "a", true},
	{[]string{"a"}, "b", false},
	{[]string{"a", "b"}, "b", false},
	{[]string{"a", "b"}, "b", false},
	{[]string{"a", "b"}, "b", true},
	{[]string{"b", "a"}, "c", false},
	{[]string{"b", "a"}, "c", false},
	{[]string{"b", "a"}, "c", true},
	{[]string{"c", "b"}, "c", true},
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFrequent(t *testing.T) {
	f := NewFrequent(2)
	for i, x := range tests {
		res := f.Get()
		if !eq(res, x.expect) {
			t.Error(i, ":", res, "is not", x.expect)
		}
		f.Add(x.add)
		if x.process {
			f.process()
		}
	}
}

func TestRecent(t *testing.T) {
	r := NewRecent(3)
	it := r.Get()
	if !eq(it, []string{ }) {
		t.Fatal("recent wrong 0")
	}
	r.Add("a")
	r.Add("b")
	r.Add("c")
	r.Add("d")
	it = r.Get()
	if !eq(it, []string{ "d", "c", "b" }) {
		t.Fatal("recent wrong 1")
	}
	r.Add("c")
	it = r.Get()
	if !eq(it, []string{ "c", "d", "b" }) {
		t.Fatal("recent wrong 2")
	}
}
