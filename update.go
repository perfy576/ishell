package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releaseRepository = "perfy576/ishell"

type githubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func runUpdate() {
	release, err := latestRelease()
	if err != nil {
		fatal(err)
	}
	if !isVersionNewer(release.TagName, appVersion) {
		fmt.Println("iShell is already up to date (v" + appVersion + ").")
		return
	}
	asset, checksumAsset, err := releaseAssets(release)
	if err != nil {
		fatal(err)
	}
	if !confirm("Update iShell from v" + appVersion + " to " + release.TagName) {
		return
	}
	archivePath, err := downloadReleaseAsset(asset)
	if err != nil {
		fatal(err)
	}
	defer os.Remove(archivePath)
	checksums, err := downloadReleaseAsset(checksumAsset)
	if err != nil {
		fatal(err)
	}
	defer os.Remove(checksums)
	if err := verifyReleaseChecksum(archivePath, checksums, asset.Name); err != nil {
		fatal(err)
	}
	current, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	target, err := updateDestination(current)
	if err != nil {
		fatal(err)
	}
	staged, err := extractReleaseExecutable(archivePath, filepath.Dir(target))
	if err != nil {
		fatal(err)
	}
	message, err := replaceRunningExecutable(staged, target)
	if err != nil {
		fatal(err)
	}
	fmt.Println(message)
}

func latestRelease() (githubRelease, error) {
	request, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+releaseRepository+"/releases/latest", nil)
	if err != nil {
		return githubRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", appName+"/"+appVersion)
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return githubRelease{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("check latest release: %s", response.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	if release.TagName == "" {
		return githubRelease{}, errors.New("latest release has no version tag")
	}
	return release, nil
}

func releaseAssets(release githubRelease) (githubReleaseAsset, githubReleaseAsset, error) {
	var binary, checksums githubReleaseAsset
	for _, asset := range release.Assets {
		if asset.Name == "checksums.txt" {
			checksums = asset
		}
		if releaseAssetMatches(asset.Name, release.TagName, runtime.GOOS, runtime.GOARCH) {
			binary = asset
		}
	}
	if binary.BrowserDownloadURL == "" {
		return githubReleaseAsset{}, githubReleaseAsset{}, fmt.Errorf("release %s has no binary for %s/%s", release.TagName, runtime.GOOS, runtime.GOARCH)
	}
	if checksums.BrowserDownloadURL == "" {
		return githubReleaseAsset{}, githubReleaseAsset{}, errors.New("release has no checksums.txt asset")
	}
	return binary, checksums, nil
}

func releaseAssetMatches(name, version, goos, goarch string) bool {
	name = strings.ToLower(name)
	version = strings.TrimPrefix(strings.ToLower(version), "v")
	if !strings.HasSuffix(name, ".zip") || !strings.Contains(name, "_"+version+"_") || !strings.Contains(name, "_"+goos+"_") {
		return false
	}
	for _, arch := range releaseArchitectures(goarch) {
		if strings.Contains(name, "_"+arch+".zip") {
			return true
		}
	}
	return false
}

func releaseArchitectures(goarch string) []string {
	switch goarch {
	case "amd64":
		return []string{"amd64", "x86_64"}
	case "arm64":
		return []string{"arm64", "aarch64"}
	default:
		return []string{goarch}
	}
}

func downloadReleaseAsset(asset githubReleaseAsset) (string, error) {
	request, err := http.NewRequest(http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", appName+"/"+appVersion)
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", asset.Name, response.Status)
	}
	file, err := os.CreateTemp("", appName+"-update-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	_, copyErr := io.Copy(file, io.LimitReader(response.Body, 256<<20))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(path)
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	return path, nil
}

func verifyReleaseChecksum(archivePath, checksumsPath, assetName string) error {
	contents, err := os.ReadFile(checksumsPath)
	if err != nil {
		return err
	}
	var expected string
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == assetName {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksum for %s is missing", assetName)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected) {
		return errors.New("release checksum does not match")
	}
	return nil
}

func extractReleaseExecutable(archivePath, directory string) (string, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer archive.Close()
	name := "ishell"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, entry := range archive.File {
		if filepath.Base(entry.Name) != name || entry.FileInfo().IsDir() {
			continue
		}
		input, err := entry.Open()
		if err != nil {
			return "", err
		}
		staged := filepath.Join(directory, "."+name+".update")
		output, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0700)
		if err != nil {
			input.Close()
			return "", err
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, 256<<20))
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil || closeInputErr != nil || closeOutputErr != nil {
			os.Remove(staged)
			if copyErr != nil {
				return "", copyErr
			}
			if closeInputErr != nil {
				return "", closeInputErr
			}
			return "", closeOutputErr
		}
		return staged, nil
	}
	return "", fmt.Errorf("release archive does not contain %s", name)
}

func isVersionNewer(candidate, current string) bool {
	candidateParts, candidateOK := parseVersion(candidate)
	currentParts, currentOK := parseVersion(current)
	if !candidateOK || !currentOK {
		return false
	}
	for index := range candidateParts {
		if candidateParts[index] != currentParts[index] {
			return candidateParts[index] > currentParts[index]
		}
	}
	return false
}

func parseVersion(value string) ([3]int, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var parsed [3]int
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return [3]int{}, false
		}
		parsed[index] = value
	}
	return parsed, true
}
