package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ISC-19: a standing notice is an hm/callout and an action result an hm/flash (Inline); page
// and layout templates never hand-write the .hm-flash markup. (hm/flash itself lives in
// partials/flash.html.)
func TestPagesDoNotHandWriteFlash(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"pages", "layouts"} {
		root := filepath.Join("..", "..", "web", "admin", dir)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), `class="hm-flash`) {
				t.Errorf("%s hand-writes hm-flash; use hm/callout (standing notice) or hm/flash with Inline", p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A success callout is a confirmation of something that just happened, which is an inline
// flash (role=status), never a standing notice. users/link.html was the offender.
func TestPagesHaveNoSuccessCallout(t *testing.T) {
	t.Parallel()

	re := regexp.MustCompile(`hm/callout-start"?\s*\(?dict\s+"Kind"\s+"success"`)
	root := filepath.Join("..", "..", "web", "admin", "pages")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if re.Match(b) {
			t.Errorf("%s shows a success action result as a callout; use hm/flash with Inline", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The progress page already prints the wizard position through hm/steps ("Step n of N");
// its own phase counter must not reuse that wording.
func TestImportProgressHasOneStepCounter(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/pages/import/progress.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Step {{") {
		t.Error("progress.html prints a second \"Step n of N\" next to hm/steps")
	}
}
