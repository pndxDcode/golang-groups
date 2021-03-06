//  Copyright 2011 The Go Authors.  All rights reserved.
//  Use of this source code is governed by a BSD-style
//  license that can be found in the LICENSE file.

//  The backend in step 5 speeds up the program by fetching all the groups from
//  the meetup API concurrently.
package backend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"appengine"
	"appengine/memcache"
	"appengine/urlfetch"
)

func init() {
	http.HandleFunc("/api/groups", getGroups)
}

var ids = []string{
	"golangsf",
	"golangsv",
	"golang-paris",
	"Los-Angeles-Gophers",
	"golang-syd",
	"golang-users-berlin",
	"bostongolang",
	"Tokyo-Golang-Developers",
	"Go-User-Group-Hamburg",
}

type Group struct {
	Name    string
	URL     string
	Members int
	City    string
	Country string
}

func getGroups(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	var res struct {
		Groups []*Group
		Errors []string
	}

	type partial struct {
		id    string
		group *Group
		err   error
	}

	partials := make(chan partial)

	// let's fetch every group concurrently
	for _, id := range ids {
		go func(id string) {
			group, err := load(c, id)
			partials <- partial{id, group, err}
		}(id)
	}

	// and get the results when they're ready
	for _ = range ids {
		p := <-partials
		if p.err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("fetch %v: %v", p.id, p.err))
			continue
		}
		res.Groups = append(res.Groups, p.group)
	}

	// then we encode it as JSON on the response
	enc := json.NewEncoder(w)
	err := enc.Encode(res)

	// And if encoding fails we log the error
	if err != nil {
		c.Errorf("encode response: %v", err)
	}
}

func load(c appengine.Context, id string) (*Group, error) {
	group := &Group{}
	_, err := memcache.JSON.Get(c, id, group)
	if err == nil {
		return group, nil
	}
	if err != memcache.ErrCacheMiss {
		c.Errorf("memcache get %q: %v", id, err)
	}

	group, err = fetch(c, id)
	if err != nil {
		return nil, err
	}

	item := &memcache.Item{
		Key:        id,
		Object:     group,
		Expiration: time.Hour,
	}
	err = memcache.JSON.Set(c, item)
	if err != nil {
		c.Errorf("memcache set %q: %v", id, err)
	}
	return group, nil
}

// fetch fetches a meetup group given its id from using the meetup API
// docs for the API: http://www.meetup.com/meetup_api/docs/
func fetch(c appengine.Context, id string) (*Group, error) {
	const (
		apiKey      = "obtain your apikey from https://secure.meetup.com/meetup_api/key/"
		urlTemplate = "https://api.meetup.com/%s?sign=true&key=%s"
	)

	client := urlfetch.Client(c)
	res, err := client.Get(fmt.Sprintf(urlTemplate, id, apiKey))
	if err != nil {
		return nil, fmt.Errorf("get: %v", err)
	}

	var g struct {
		Name    string `json:"name"`
		Link    string `json:"link"`
		City    string `json:"city"`
		Country string `json:"country"`
		Members int    `json:"members"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	dec := json.NewDecoder(res.Body)
	err = dec.Decode(&g)
	if err != nil {
		return nil, fmt.Errorf("decode: %v", err)
	}

	if len(g.Errors) > 0 {
		var errs []string
		for _, e := range g.Errors {
			errs = append(errs, e.Message)
		}
		return nil, fmt.Errorf(strings.Join(errs, "\n"))
	}

	return &Group{
		Name:    g.Name,
		URL:     g.Link,
		Members: g.Members,
		City:    g.City,
		Country: g.Country,
	}, nil

}
