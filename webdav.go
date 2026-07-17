package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (c webDAVConfig) configured() bool {
	return strings.TrimSpace(c.URL) != ""
}

func webDAVURL(config webDAVConfig, parts ...string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(config.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("WebDAV URL must be an absolute http or https URL")
	}
	paths := []string{u.Path, config.Path}
	paths = append(paths, parts...)
	u.Path = pathpkg.Join(paths...)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	return u.String(), nil
}

func webDAVRequest(config webDAVConfig, method string, target string, body io.Reader, depth string) (*http.Response, error) {
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, err
	}
	if config.Username != "" || config.Password != "" {
		request.SetBasicAuth(config.Username, config.Password)
	}
	if depth != "" {
		request.Header.Set("Depth", depth)
	}
	return (&http.Client{Timeout: 20 * time.Second}).Do(request)
}

func ensureWebDAVCollection(config webDAVConfig, parts ...string) error {
	target, err := webDAVURL(config, parts...)
	if err != nil {
		return err
	}
	response, err := webDAVRequest(config, "MKCOL", target, nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusCreated || response.StatusCode == http.StatusMethodNotAllowed {
		return nil
	}
	return fmt.Errorf("create WebDAV collection: %s", response.Status)
}

func uploadWebDAVBackup(config webDAVConfig, name string, contents []byte, maximum int) error {
	if !config.configured() {
		return nil
	}
	if !isBackupName(name) {
		return errors.New("invalid backup name")
	}
	if err := ensureWebDAVCollection(config); err != nil {
		return err
	}
	if err := ensureWebDAVCollection(config, name); err != nil {
		return err
	}
	target, err := webDAVURL(config, name, "vault.json")
	if err != nil {
		return err
	}
	response, err := webDAVRequest(config, "PUT", target, bytes.NewReader(contents), "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("upload WebDAV backup: %s", response.Status)
	}
	return pruneWebDAVBackups(config, maximum)
}

type webDAVMultiStatus struct {
	Responses []struct {
		Href string `xml:"href"`
	} `xml:"response"`
}

func listWebDAVBackups(config webDAVConfig) ([]string, error) {
	if !config.configured() {
		return nil, nil
	}
	target, err := webDAVURL(config)
	if err != nil {
		return nil, err
	}
	response, err := webDAVRequest(config, "PROPFIND", target, strings.NewReader(`<?xml version="1.0"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`), "1")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 207 {
		return nil, fmt.Errorf("list WebDAV backups: %s", response.Status)
	}
	var result webDAVMultiStatus
	if err := xml.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, response := range result.Responses {
		href, err := url.PathUnescape(response.Href)
		if err != nil {
			continue
		}
		name := pathpkg.Base(strings.TrimSuffix(href, "/"))
		if isBackupName(name) {
			seen[name] = true
		}
	}
	var names []string
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func pruneWebDAVBackups(config webDAVConfig, maximum int) error {
	if maximum <= 0 || !config.configured() {
		return nil
	}
	names, err := listWebDAVBackups(config)
	if err != nil {
		return err
	}
	for len(names) > maximum {
		target, err := webDAVURL(config, names[0])
		if err != nil {
			return err
		}
		response, err := webDAVRequest(config, "DELETE", target, nil, "")
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("delete WebDAV backup: %s", response.Status)
		}
		names = names[1:]
	}
	return nil
}

func pullNewWebDAVBackups(config webDAVConfig, localDir string, maximum int) (int, error) {
	names, err := listWebDAVBackups(config)
	if err != nil || len(names) == 0 {
		return 0, err
	}
	pulled := 0
	for _, name := range names {
		localPath := filepath.Join(localDir, name, "vault.json")
		if _, err := os.Stat(localPath); err == nil {
			continue
		}
		target, err := webDAVURL(config, name, "vault.json")
		if err != nil {
			return pulled, err
		}
		response, err := webDAVRequest(config, "GET", target, nil, "")
		if err != nil {
			return pulled, err
		}
		contents, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		response.Body.Close()
		if readErr != nil {
			return pulled, readErr
		}
		if response.StatusCode != http.StatusOK {
			return pulled, fmt.Errorf("download WebDAV backup: %s", response.Status)
		}
		if err := writePrivateFile(filepath.Join(localDir, name, "vault.json"), contents); err != nil {
			return pulled, err
		}
		pulled++
	}
	return pulled, pruneBackups(localDir, maximum)
}
