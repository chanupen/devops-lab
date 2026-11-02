package labs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const readme = "# Lab 9: Something\n\nIntro line.\n\n" +
	"## Step 1: Do a thing\n\nThe task.\n\n```bash\n## Step 9: not a heading inside a fence\n```\n\n" +
	"### Hints\n- first hint\n- second hint\n  continues here\n\n### Expected result\nIt is done.\n\n" +
	"## Step 2: Another\n\nTask two.\n\n### Hints\n- only hint\n\n### Expected result\nAlso done.\n"

func TestParseReadme(t *testing.T) {
	intro, steps := ParseReadme(readme)
	if intro != "Intro line." {
		t.Errorf("intro = %q", intro)
	}
	if len(steps) != 2 {
		t.Fatalf("%d steps", len(steps))
	}
	s := steps[0]
	if s.N != 1 || s.Title != "Do a thing" || !strings.HasPrefix(s.Task, "The task.") || !strings.Contains(s.Task, "## Step 9: not a heading") {
		t.Errorf("step 1 = %+v", s)
	}
	if len(s.Hints) != 2 || s.Hints[1] != "second hint\ncontinues here" || s.Expected != "It is done." {
		t.Errorf("step 1 hints %q expected %q", s.Hints, s.Expected)
	}
	if steps[1].Title != "Another" || len(steps[1].Hints) != 1 {
		t.Errorf("step 2 = %+v", steps[1])
	}
}

// writeLab makes a lab directory with n steps.
func writeLab(t *testing.T, root, id string, n int, needs string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	files := map[string]string{
		"lab.yaml":  "id: " + id + "\ntitle: Test lab\ntime: 5m\nlevel: beginner\nmodes: [local]\nneeds: " + needs + "\nstacks: [core]\n",
		"reset.sh":  "#!/bin/sh\n",
		"README.md": "# Test\n\nIntro.\n",
	}
	for i := 1; i <= n; i++ {
		files["README.md"] += "\n## Step " + itoa(i) + ": Step " + itoa(i) + "\n\nDo it.\n\n### Hints\n- h1\n- h2\n\n### Expected result\nDone.\n"
		files["steps/"+pad(i)+"-verify.sh"] = "#!/bin/sh\n"
		files["solution/"+pad(i)+"-solve.sh"] = "#!/bin/sh\necho solve " + itoa(i) + "\n"
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func itoa(i int) string { return string(rune('0' + i)) }
func pad(i int) string  { return "0" + itoa(i) }

func TestLoadChecksTheLab(t *testing.T) {
	root := t.TempDir()
	dir := writeLab(t, root, "01-test", 2, "scratch")
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Number() != "01" || len(l.Steps) != 2 || l.VerifyScript(2) != filepath.Join(dir, "steps", "02-verify.sh") {
		t.Errorf("lab = %+v", l)
	}
	if err := os.Remove(filepath.Join(dir, "steps", "02-verify.sh")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "02-verify.sh") {
		t.Errorf("missing verify script: %v", err)
	}
	bad := writeLab(t, root, "02-bad", 1, "somewhere")
	if _, err := Load(bad); err == nil || !strings.Contains(err.Error(), "needs is") {
		t.Errorf("bad needs: %v", err)
	}
}

// The labs in the repo load and follow the format.
func TestRepoLabs(t *testing.T) {
	ls, err := LoadAll(filepath.Join("..", "..", "labs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) == 0 || ls[0].ID != "01-argocd-bootstrap" {
		t.Fatalf("labs = %v", ls)
	}
	l := ls[0]
	if l.Needs != NeedsScratch || len(l.Steps) != 6 || l.Title != "Bootstrap Argo CD from scratch" {
		t.Errorf("lab 1 = %s needs %s, %d steps", l.Title, l.Needs, len(l.Steps))
	}
	for _, s := range l.Steps {
		for _, f := range []string{l.VerifyScript(s.N), l.SolveScript(s.N)} {
			if fi, err := os.Stat(f); err != nil || fi.Mode()&0o100 == 0 {
				t.Errorf("%s: not an executable file (%v)", f, err)
			}
		}
	}
}
