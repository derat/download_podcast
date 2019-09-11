// Copyright 2019 Daniel Erat. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package main

import (
	"encoding/xml"
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
	"strings"

	_ "crypto/sha512"
)

const (
	// Subdirectory in the destination dir used to track already-downloaded files.
	seenSubdir = ".seen"
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

func openUrl(u string) (io.ReadCloser, error) {
	resp, err := http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %v: %v", u, err)
	} else if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server returned %v for %v", resp.StatusCode, u)
	}
	return resp.Body, nil
}

func getUrls(feed string) ([]string, error) {
	body, err := openUrl(feed)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	d := xml.NewDecoder(body)
	d.Strict = false

	urls := make(map[string]bool)
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		e, ok := t.(xml.StartElement)
		if !ok {
			continue
		}

		if e.Name.Local == "media:content" || e.Name.Local == "enclosure" {
			for _, a := range e.Attr {
				if a.Name.Local == "url" {
					urls[a.Value] = true
					break
				}
			}
		}
	}

	u := make([]string, len(urls), len(urls))
	i := 0
	for v, _ := range urls {
		u[i] = v
		i++
	}
	return u, nil
}

func downloadUrl(u, destDir, prefix string, verbose, skipDownload bool) error {
	base := path.Base(u)
	if i := strings.IndexByte(base, '?'); i != -1 {
		base = base[0:i]
	}

	if len(base) == 0 || base == "." || base == ".." {
		return fmt.Errorf("unable to get valid filename from %v", u)
	}
	if err := os.MkdirAll(filepath.Join(destDir, seenSubdir), 0755); err != nil {
		return err
	}

	seenPath := filepath.Join(destDir, seenSubdir, url.PathEscape(u))
	oldSeenPath := filepath.Join(destDir, seenSubdir, base)
	exists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	if exists(seenPath) || exists(oldSeenPath) {
		if verbose {
			log.Printf("Skipping %v", u)
		}
		return nil
	}

	if !skipDownload {
		destPath := filepath.Join(destDir, prefix+base)
		if verbose {
			log.Printf("Downloading %v to %v", u, destPath)
		}
		body, err := openUrl(u)
		if err != nil {
			return err
		}
		defer body.Close()

		f, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err = io.Copy(f, body); err != nil {
			return err
		}
	}

	if verbose {
		log.Printf("Touching %v", seenPath)
	}
	sf, err := os.Create(seenPath)
	if err != nil {
		return err
	}
	sf.Close()
	return nil
}

func main() {
	var feed, dest, prefix string
	var quiet, skip bool
	var num int
	flag.StringVar(&dest, "dest", filepath.Join(os.Getenv("HOME"), "temp", "podcasts"), "Directory where files should be saved")
	flag.StringVar(&feed, "feed", "", "URL of feed to mirror")
	flag.StringVar(&prefix, "prefix", "", "Prefix to prepend to filenames")
	flag.BoolVar(&quiet, "quiet", false, "Suppress informational logging")
	flag.BoolVar(&skip, "skip", false, "Mark files as downloaded without downloading")
	flag.IntVar(&num, "num", -1, "Maximum number of files to mirror")
	flag.Parse()

	urls, err := getUrls(feed)
	if err != nil {
		log.Fatalf("Failed to extract URLs from %v: %v", feed, err)
	}

	for i, u := range urls {
		if num >= 0 && i >= num {
			break
		}
		if err = downloadUrl(u, dest, prefix, !quiet, skip); err != nil {
			log.Printf("Failed to download %v: %v", u, err)
		}
	}
}
