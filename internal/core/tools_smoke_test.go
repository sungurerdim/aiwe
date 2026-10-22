//go:build smoke

package core_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestToolsManifestMatchesLiveRelease catches a pin whose sha256 or licence disagrees with the
// checksum file and the LICENSE the release publishes (K7). The reads are live, so they stay
// behind the smoke tag and off the default test path (SPEC §5.13).
func TestToolsManifestMatchesLiveRelease(t *testing.T) {
	data, err := os.ReadFile("tools.json")
	if err != nil {
		t.Fatalf("read the pinned tool table: %v", err)
	}
	pins, err := core.ParseToolPins(data)
	if err != nil {
		t.Fatalf("ParseToolPins(tools.json) = %v, want nil", err)
	}
	seen := map[string]bool{}
	for _, pin := range pins.Tools {
		seen[pin.Name] = true
		docs, ferr := toolTestLiveDocs(t, pin)
		if ferr != nil {
			t.Errorf("read the live release of %s: %v", pin.Name, ferr)
			continue
		}
		if cerr := core.ConfirmToolPin(pin, docs); cerr != nil {
			t.Errorf("%s: %v", pin.Name, cerr)
		}
	}
	for _, name := range []string{"gremlins", "grype", "gitleaks"} {
		if !seen[name] {
			t.Errorf("tools.json does not pin %s", name)
		}
	}
}

func toolTestLiveDocs(t *testing.T, pin core.ToolPin) (core.ReleaseDocs, error) {
	t.Helper()
	owner, repo, tag, asset, base, err := toolTestRelease(pin)
	if err != nil {
		return core.ReleaseDocs{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sums, err := toolTestFirst(ctx, asset, []string{
		base + "checksums.txt",
		base + pin.Name + "_" + pin.Version + "_checksums.txt",
	})
	if err != nil {
		return core.ReleaseDocs{}, err
	}
	raw := "https://raw.githubusercontent.com/" + owner + "/" + repo + "/" + tag + "/"
	licence, err := toolTestFirst(ctx, "", []string{raw + "LICENSE", raw + "LICENSE.md", raw + "COPYING"})
	if err != nil {
		return core.ReleaseDocs{}, err
	}
	return core.ReleaseDocs{Checksums: sums, LicenceText: licence}, nil
}

func toolTestRelease(pin core.ToolPin) (owner, repo, tag, asset, base string, err error) {
	const prefix = "https://github.com/"
	parts := strings.Split(strings.TrimPrefix(pin.Source, prefix), "/")
	slash := strings.LastIndex(pin.URL, "/")
	ok := strings.HasPrefix(pin.Source, prefix) && len(parts) == 5 && parts[2] == "releases" &&
		parts[3] == "tag" && parts[4] == "v"+pin.Version && slash > 0 && slash < len(pin.URL)-1 &&
		!strings.Contains(parts[0]+parts[1], "..")
	if !ok {
		return "", "", "", "", "", fmt.Errorf("source %q is not the github tag of version %s", pin.Source, pin.Version)
	}
	owner, repo, tag, asset = parts[0], parts[1], parts[4], pin.URL[slash+1:]
	base = prefix + owner + "/" + repo + "/releases/download/" + tag + "/"
	if pin.URL != base+asset {
		return "", "", "", "", "", fmt.Errorf("url %q is not the release asset of %s", pin.URL, pin.Source)
	}
	return owner, repo, tag, asset, base, nil
}

func toolTestFirst(ctx context.Context, marker string, urls []string) (string, error) {
	var last error
	for _, u := range urls {
		body, err := toolTestGet(ctx, u)
		if err != nil {
			last = err
			continue
		}
		if marker == "" || strings.Contains(body, marker) {
			return body, nil
		}
		last = fmt.Errorf("%s does not name %s\n%s", u, marker, body)
	}
	if last == nil {
		last = errors.New("no document")
	}
	return "", last
}

func toolTestGet(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if len(body) > 1<<20 || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %s, %d bytes", url, resp.Status, len(body))
	}
	return string(body), nil
}
