// Copyright 2019 Daniel Erat. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package main

import (
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	_ "crypto/sha512"
)

const (
	seenSubdir     = ".seen" // dest dir subdir for tracking already-downloaded files
	maxFilenameLen = 255     // max length for path components
)

func getMatch(re, s string) (string, error) {
	r, err := regexp.Compile(re)
	if err != nil {
		return "", fmt.Errorf("couldn't compile %q", re)
	}
	m := r.Find([]byte(s))
	if m == nil {
		return "", fmt.Errorf("didn't find %q", re)
	}
	return string(m), nil
}

func openURL(u string) (io.ReadCloser, error) {
	resp, err := http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %v: %v", u, err)
	} else if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("server returned %v for %v", resp.StatusCode, u)
	}
	return resp.Body, nil
}

type item struct{ guid, url, title string }

func getItems(feed string) ([]item, error) {
	body, err := openURL(feed)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	d := xml.NewDecoder(body)
	d.Strict = false

	var items []item
	var inGUID, inTitle bool
	var guid, title, url string

	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		switch e := t.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "item":
				guid = ""
				title = ""
				url = ""
			case "guid":
				inGUID = true
			case "title":
				inTitle = true
			case "media:content", "enclosure":
				for _, a := range e.Attr {
					if a.Name.Local == "url" {
						url = a.Value
						break
					}
				}
			}

		case xml.EndElement:
			switch e.Name.Local {
			case "item":
				if url != "" {
					if guid == "" {
						guid = url
					}
					items = append(items, item{guid, url, title})
				}
			case "guid":
				inGUID = false
			case "title":
				inTitle = false
			}

		case xml.CharData:
			switch {
			case inGUID:
				guid = string(e)
			case inTitle:
				title = string(e)
			}
		}
	}

	return items, nil
}

// Simplecast uses bullshit URLs like the following:
// https://dts.podtrac.com/redirect.mp3/nyt.simplecastaudio.com/521189a6-a4f6-404d-85cf-455a989a10a4/episodes/4a49fb56-5d6d-4800-8b83-72047d6b81e7/audio/128/default.mp3?aid=rss_feed&awCollectionId=521189a6-a4f6-404d-85cf-455a989a10a4&awEpisodeId=4a49fb56-5d6d-4800-8b83-72047d6b81e7&feed=xl36XBC2
// Grab the episode ID so we don't try to name everything default.mp3.
var episodeIDRegexp = regexp.MustCompile(`/episodes/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/`)

func downloadItem(item item, destDir, feed, prefix string, verbose, skipDownload bool) error {
	base := path.Base(item.url)
	if i := strings.IndexByte(base, '?'); i != -1 {
		base = base[:i]
	}

	// If this is a crappy Simplecast URL, use the title from the feed if we have it
	// before falling back to the UUID.
	if m := episodeIDRegexp.FindStringSubmatch(item.url); m != nil {
		if item.title != "" {
			base = item.title + ".mp3"
		} else {
			base = m[1] + ".mp3"
		}
	}

	if len(base) == 0 || base == "." || base == ".." {
		return errors.New("unable to get valid filename")
	}

	// Check if we've already seen this item.
	seenPath := filepath.Join(destDir, seenSubdir, escape(feed), escape(item.guid))
	if err := os.MkdirAll(filepath.Dir(seenPath), 0755); err != nil {
		return err
	}
	for _, p := range []string{
		seenPath,
		filepath.Join(destDir, seenSubdir, escape(item.url)), // old location
		filepath.Join(destDir, seenSubdir, escape(base)),     // really old location
	} {
		if _, err := os.Stat(p); err == nil {
			if verbose {
				log.Printf("Skipping %v (%v %q)", item.url, item.guid, item.title)
			}
			if _, err := os.Stat(seenPath); os.IsNotExist(err) {
				touch(seenPath) // migrate to new location
			}
			return nil
		}
	}

	destPath := filepath.Join(destDir, prefix+base)
	if _, err := os.Stat(destPath); err == nil {
		// If the base filename already exists, append a number to its pre-extension part.
		ext := filepath.Ext(base)
		start := base[:len(base)-len(ext)]
		for i := 0; i >= 0; i++ {
			destPath = filepath.Join(destDir, prefix+start+strconv.Itoa(i)+ext)
			if _, err := os.Stat(destPath); err != nil {
				break // found an unused filename
			}
		}
	}

	if skipDownload {
		if verbose {
			log.Printf("Skipping download of %v (%v) to %v", item.url, item.title, destPath)
		}
	} else {
		if verbose {
			log.Printf("Downloading %v (%v) to %v", item.url, item.title, destPath)
		}
		if err := download(item.url, destPath); err != nil {
			return err
		}
	}

	if verbose {
		log.Printf("Touching %v", seenPath)
	}
	return touch(seenPath)
}

// escape escapes fn so it can be used as a path component.
func escape(fn string) string {
	esc := url.PathEscape(fn)
	if len(esc) > maxFilenameLen {
		esc = esc[:maxFilenameLen]
	}
	return esc
}

// touch creates an empty file at p.
func touch(p string) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	return f.Close()
}

// download downloads url to p.
func download(url, p string) error {
	body, err := openURL(url)
	if err != nil {
		return err
	}
	defer body.Close()

	f, err := os.Create(p)
	if err != nil {
		return err
	}
	if _, err = io.Copy(f, body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func main() {
	dest := flag.String("dest", filepath.Join(os.Getenv("HOME"), "temp/podcasts"), "Directory where files should be saved")
	feed := flag.String("feed", "", "URL of feed to mirror")
	prefix := flag.String("prefix", "", "Prefix to prepend to filenames")
	quiet := flag.Bool("quiet", false, "Suppress informational logging")
	skip := flag.Bool("skip", false, "Mark files as downloaded without downloading")
	num := flag.Int("num", -1, "Maximum number of files to mirror")
	flag.Parse()

	if *feed == "" {
		log.Fatal("-feed must be supplied")
	}
	items, err := getItems(*feed)
	if err != nil {
		log.Fatalf("Failed to extract items from %v: %v", *feed, err)
	}

	for i, item := range items {
		if *num >= 0 && i >= *num {
			break
		}
		if err = downloadItem(item, *dest, *feed, *prefix, !*quiet, *skip); err != nil {
			log.Printf("Failed to download %v: %v", item.url, err)
		}
	}
}
