package main

import (
	"archive/zip"
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
	"strconv"
	"strings"
	"time"
)

type webDAVArchive struct {
	Name string
}

func (c webDAVConfig) configured() bool {
	return c.enabled() && strings.TrimSpace(c.URL) != ""
}

func (c webDAVConfig) enabled() bool {
	// Configurations written before the toggle was introduced remain enabled.
	return c.Enabled == nil || *c.Enabled
}

func webDAVURL(config webDAVConfig, parts ...string) (string, error) {
	baseURL := normalizeWebDAVURL(config.URL)
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("WebDAV URL must be an absolute http or https URL (received %q)", baseURL)
	}
	paths := []string{u.Path, config.Path}
	paths = append(paths, parts...)
	u.Path = pathpkg.Join(paths...)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	return u.String(), nil
}

func normalizeWebDAVURL(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer(
		"\x00", "",
		"\ufeff", "",
		"\u200b", "",
		"\u200c", "",
		"\u200d", "",
		"\u2060", "",
		"\uff1a", ":",
		"\uff0f", "/",
	).Replace(value)
	return strings.TrimSpace(value)
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

func webDAVCollectionURL(config webDAVConfig, parts ...string) (string, error) {
	target, err := webDAVURL(config, parts...)
	if err != nil {
		return "", err
	}
	collection, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	collection.Path = strings.TrimRight(collection.Path, "/") + "/"
	return collection.String(), nil
}

func ensureWebDAVCollection(config webDAVConfig, parts ...string) error {
	target, err := webDAVCollectionURL(config, parts...)
	if err != nil {
		return err
	}

	response, err := webDAVRequest(config, "PROPFIND", target, strings.NewReader(`<?xml version="1.0"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`), "0")
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("access WebDAV collection: %s", response.Status)
	}

	response, err = webDAVRequest(config, "MKCOL", target, nil, "")
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

func webDAVArchiveName(config webDAVConfig, at time.Time, label string) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "local"
	}
	username := config.Username
	if username == "" {
		username = "default"
	}
	if label == "" {
		label = "auto"
	}
	return safeArchiveNamePart(host) + "-" + safeArchiveNamePart(username) + "-" + at.Format("20060102150405") + "-" + label + ".zip"
}

func validateBackupLabel(value string) (string, error) {
	value = strings.TrimSpace(removeNullCharacters(value))
	if value == "" {
		return "manual", nil
	}
	if strings.TrimRight(value, ". ") != value || value == "." || value == ".." {
		return "", errors.New("backup label cannot end with a space or period")
	}
	if len([]rune(value)) > 64 {
		return "", errors.New("backup label must be at most 64 characters")
	}
	for _, character := range value {
		if character < 32 || strings.ContainsRune(`<>:"/\\|?*`, character) {
			return "", errors.New("backup label contains a character not supported by Windows, Linux, or WebDAV")
		}
	}
	return value, nil
}

func safeArchiveNamePart(value string) string {
	value = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, strings.TrimSpace(value))
	if value == "" {
		return "default"
	}
	return value
}

func archiveVault(contents []byte) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create("vault.json")
	if err != nil {
		return nil, err
	}
	if _, err := entry.Write(contents); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func extractVaultArchive(contents []byte) ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return nil, err
	}
	for _, entry := range archive.File {
		if entry.Name != "vault.json" || entry.FileInfo().IsDir() {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, err
		}
		vault, readErr := io.ReadAll(io.LimitReader(reader, 16<<20))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return vault, nil
	}
	return nil, errors.New("backup archive does not contain vault.json")
}

func uploadWebDAVArchive(config webDAVConfig, name string, contents []byte, maximum int) error {
	if !config.configured() {
		return nil
	}
	if !strings.HasSuffix(name, ".zip") {
		return errors.New("invalid backup archive name")
	}
	if err := ensureWebDAVCollection(config); err != nil {
		return err
	}
	target, err := webDAVURL(config, name)
	if err != nil {
		return err
	}
	response, err := webDAVRequest(config, "PUT", target, bytes.NewReader(contents), "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("upload WebDAV archive: %s", response.Status)
	}
	return pruneWebDAVArchives(config, maximum)
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
	target, err := webDAVCollectionURL(config)
	if err != nil {
		return nil, err
	}
	response, err := webDAVRequest(config, "PROPFIND", target, strings.NewReader(`<?xml version="1.0"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`), "1")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
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

func listWebDAVArchives(config webDAVConfig) ([]webDAVArchive, error) {
	if !config.configured() {
		return nil, errors.New("WebDAV is disabled or not configured")
	}
	target, err := webDAVCollectionURL(config)
	if err != nil {
		return nil, err
	}
	response, err := webDAVRequest(config, "PROPFIND", target, strings.NewReader(`<?xml version="1.0"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`), "1")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != 207 {
		return nil, fmt.Errorf("list WebDAV archives: %s", response.Status)
	}
	var result webDAVMultiStatus
	if err := xml.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var archives []webDAVArchive
	for _, item := range result.Responses {
		href, err := url.PathUnescape(item.Href)
		if err != nil {
			continue
		}
		name := pathpkg.Base(strings.TrimSuffix(href, "/"))
		if strings.HasSuffix(strings.ToLower(name), ".zip") && !seen[name] {
			seen[name] = true
			archives = append(archives, webDAVArchive{Name: name})
		}
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].Name > archives[j].Name })
	return archives, nil
}

func downloadWebDAVArchive(config webDAVConfig, name string) ([]byte, error) {
	target, err := webDAVURL(config, name)
	if err != nil {
		return nil, err
	}
	response, err := webDAVRequest(config, "GET", target, nil, "")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download WebDAV archive: %s", response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, 32<<20))
}

func pruneWebDAVArchives(config webDAVConfig, maximum int) error {
	if maximum <= 0 {
		return nil
	}
	archives, err := listWebDAVArchives(config)
	if err != nil {
		return err
	}
	for len(archives) > maximum {
		target, err := webDAVURL(config, archives[len(archives)-1].Name)
		if err != nil {
			return err
		}
		response, err := webDAVRequest(config, "DELETE", target, nil, "")
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("delete WebDAV archive: %s", response.Status)
		}
		archives = archives[:len(archives)-1]
	}
	return nil
}

func testWebDAV(config webDAVConfig) error {
	if !config.configured() {
		return errors.New("WebDAV is disabled or not configured")
	}
	if err := ensureWebDAVCollection(config); err != nil {
		return err
	}
	name := ".ishell-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	target, err := webDAVURL(config, name)
	if err != nil {
		return err
	}
	response, err := webDAVRequest(config, "PUT", target, strings.NewReader("ishell"), "")
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("WebDAV write test: %s", response.Status)
	}
	response, err = webDAVRequest(config, "DELETE", target, nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("WebDAV delete test: %s", response.Status)
	}
	return nil
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
