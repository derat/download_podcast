package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
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
		return "", fmt.Errorf("Couldn't compile regular expression")
	}
	m := r.Find([]byte(s))
	if m == nil {
		return "", fmt.Errorf("Didn't find regular expression %q", re)
	}
	return string(m), nil
}

func openUrl(u string) (io.ReadCloser, error) {
	resp, err := http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch URL: %v", err)
	} else if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Server returned %v", resp.StatusCode)
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

func downloadUrl(url, destDir string, verbose, skipDownload bool) error {
	base := path.Base(url)
	if i := strings.IndexByte(base, '?'); i != -1 {
		base = base[0:i]
	}

	if len(base) == 0 || base == "." || base == ".." {
		return fmt.Errorf("Unable to get valid filename from %v", url)
	}
	if err := os.MkdirAll(filepath.Join(destDir, seenSubdir), 0755); err != nil {
		return err
	}

	seenPath := filepath.Join(destDir, seenSubdir, base)
	if _, err := os.Stat(seenPath); err == nil {
		if verbose {
			log.Printf("Skipping %v", url)
		}
		return nil
	}

	if !skipDownload {
		destPath := filepath.Join(destDir, base)
		if verbose {
			log.Printf("Downloading %v to %v", url, destPath)
		}
		body, err := openUrl(url)
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
	var feed, dest string
	var quiet, skip bool
	flag.StringVar(&dest, "dest", filepath.Join(os.Getenv("HOME"), "temp", "podcasts"), "Directory where files should be saved")
	flag.StringVar(&feed, "feed", "", "Feed to mirror")
	flag.BoolVar(&quiet, "quiet", false, "Suppress informational logging")
	flag.BoolVar(&skip, "skip", false, "Mark files as downloaded without downloading")
	flag.Parse()

	urls, err := getUrls(feed)
	if err != nil {
		log.Fatalf("Failed to extract URLs from %v: %v", feed, err)
	}
	for _, u := range urls {
		if err = downloadUrl(u, dest, !quiet, skip); err != nil {
			log.Printf("Failed to download %v: %v", u, err)
		}
	}
}
