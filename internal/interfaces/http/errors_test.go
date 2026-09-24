package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/pkg/apperr"
)

// usedCodes finds every API error code written in the code base.
func usedCodes(t *testing.T) map[string][]string {
	t.Helper()
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`errBody\("([a-z_]+)"`),
		regexp.MustCompile(`apperr\.New\("([a-z_]+)"`),
		regexp.MustCompile(`writeError\([^\n]*"([a-z_]+)"\)`),
		regexp.MustCompile(`"code":\s*"([a-z_]+)"`),
	}
	used := map[string][]string{}
	err := filepath.WalkDir("../..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		if strings.Contains(path, "pkg/apperr") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, re := range patterns {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				used[m[1]] = append(used[m[1]], path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func TestErrorCatalogMatchesCode(t *testing.T) {
	used := usedCodes(t)
	var missing, stale []string
	for code, where := range used {
		if _, ok := ErrorCatalog[code]; !ok {
			missing = append(missing, fmt.Sprintf("%s (%s)", code, where[0]))
		}
	}
	for code := range ErrorCatalog {
		if _, ok := used[code]; !ok {
			stale = append(stale, code)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("catalog out of date\nmissing: %v\nunused: %v", missing, stale)
	}
}

func TestWriteError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(err error, status int, code string) (int, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		writeError(c, err, status, code)
		var body map[string]any
		json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	status, body := run(fmt.Errorf("wrap: %w", apperr.New("image_count_exceeded", "too many", "max", 4)), 422, "submit_failed")
	if status != 422 || body["code"] != "image_count_exceeded" || body["params"].(map[string]any)["max"] != float64(4) {
		t.Fatalf("coded: %d %v", status, body)
	}
	status, body = run(apperr.New("insufficient_credits", "x"), 422, "submit_failed")
	if status != http.StatusPaymentRequired {
		t.Fatalf("catalog status not applied: %d %v", status, body)
	}
	status, body = run(errors.New("dial tcp 10.0.0.5:3306: secret detail"), 500, "internal")
	if status != 500 || body["code"] != "internal" || strings.Contains(fmt.Sprint(body), "secret") {
		t.Fatalf("server error leaked: %v", body)
	}
	status, body = run(errors.New("plain validation"), 422, "submit_failed")
	if status != 422 || body["code"] != "submit_failed" || body["message"] != "plain validation" {
		t.Fatalf("fallback: %d %v", status, body)
	}
}
