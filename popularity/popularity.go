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
	"sort"
	"sync"
	"time"
)

// Package popularity implements thread-safe data structures
// for tracking recently used strings and most frequently used strings.
// Expensive operations like sorting and trimming are delayed, so that
// calls to Get are not guaranteed to be completely accurate, but
// overhead is guaranteed to be fixed.

// A Frequent tracks the N most frequently seen strings.
type Frequent struct {
	mu        sync.Mutex
	n         int
	list      counts
	processed time.Time
	Period    time.Duration // The frequency of re-sorting the list.
}

type count struct {
	s     string
	count int
}

type counts []count

func (s counts) Len() int      { return len(s) }
func (s counts) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

type byString struct{ counts }

func (s byString) Less(i, j int) bool { return s.counts[i].s < s.counts[j].s }

type byCount struct{ counts }

func (s byCount) Less(i, j int) bool { return s.counts[i].count < s.counts[j].count }

// from the sort package examples
type reverse struct {
	sort.Interface
}

func (r reverse) Less(i, j int) bool {
	return r.Interface.Less(j, i)
}

// NewFrequent creates a new object to track the n most frequent
// strings. Period defaults to 5 minutes, and can be changed by accessing
// it directly.
func NewFrequent(n int) *Frequent {
	return &Frequent{n: n, Period: time.Minute * 5}
}

// Add adds a count for the given string.
func (f *Frequent) Add(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.list {
		if f.list[i].s == s {
			f.list[i].count++
			return
		}
	}

	// else, add a new counter for this one
	f.list = append(f.list, count{s, 1})
}

// process sorts the internal state of the object, so that the next
// call to Get will return precise results, and the amount of memory
// used by the object is minimized. The caller must hold f.mu.
func (f *Frequent) process() {
	// put the most popular at the front of the list
	sort.Sort(reverse{byCount{f.list}})

	// truncate the list
	current := f.list
	n := len(current)
	if n > f.n {
		n = f.n
	}
	f.list = make(counts, n)
	copy(f.list, current[0:n])

	// zero the counts, so that newly popular things can overtake
	// formerly popular things right away
	for i := range f.list {
		f.list[i].count = 0
	}

	f.processed = time.Now()
}

func (f *Frequent) Get() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if time.Since(f.processed) > f.Period {
		f.process()
	}

	n := len(f.list)
	if n > f.n {
		n = f.n
	}
	res := make([]string, n)
	for i := 0; i < n; i++ {
		res[i] = f.list[i].s
	}
	return res
}

type Recent struct {
	mu	sync.Mutex
	list []string
	n int
}

// NewRecent returns an object that can track the n most recently added
// strings.
func NewRecent(n int) *Recent {
	return &Recent{ n: n }
}

// Add adds a string to the top of the list, or promotes a string
// already in the list to the top.
func (r *Recent)Add(str string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.list == nil {
		r.list = make([]string, 1, r.n)
		r.list[0] = str
		return
	}

	for i,s := range r.list {
		if s == str {
			if i == 0 {
				return
			}
			// move slots 0 to here down one, then put str in 0
			copy(r.list[1:], r.list[0:i])
			r.list[0] = str
			return
		}
	}

	// str is not yet in the list, so move everything down one
	// extending the slice up towards cap if needed
	if (len(r.list) < cap(r.list)) {
		r.list = r.list[0:len(r.list)+1]
	}
	copy(r.list[1:], r.list[0:len(r.list)-1])
	r.list[0] = str
	return
}

// Get returns a copy of the current list.
func (r *Recent)Get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]string, len(r.list))
	copy(res, r.list)
	return res
}
