// Package agentcontext holds the gate over this repository's agent context
// files — the root AGENTS.md, the CLAUDE.md pointer, and the skills under
// .claude/skills.
//
// An agent reads AGENTS.md on every request, so an oversized or malformed set
// of these files is worse than none: it spends context and measurably reduces
// adherence (obin-ai/handbook#68). Two parts of that standard a machine can
// hold are the root file's length budget and each skill's frontmatter
// contract, so they are checked here rather than remembered.
//
// The frontmatter check is the load-bearing one. A skill whose name disagrees
// with its directory, or that declares no description, is simply never loaded:
// it fails nothing, surfaces nowhere, and rots invisibly.
//
// This is a test rather than a script wired into a workflow because this
// repository has no CI — `go test ./...` is the only gate anyone runs, so a
// check that is not a test is a check that does not happen.
package agentcontext

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is this package's fixed depth below the module root.
const repoRoot = "../.."

const (
	agentContextFile   = "AGENTS.md"
	claudePointerFile  = "CLAUDE.md"
	maxRootLines       = 200
	maxSkillBodyLines  = 500
	maxNameChars       = 64
	maxDescriptionSize = 1024
)

var skillNameShape = regexp.MustCompile(`^[a-z0-9-]+$`)

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func TestAgentContextRootStaysWithinItsBudget(t *testing.T) {
	path := filepath.Join(repoRoot, agentContextFile)
	if lines := len(readLines(t, path)); lines > maxRootLines {
		t.Errorf("%s is %d lines, over the %d-line cap. Move procedure into "+
			".claude/skills/, or context into a nested AGENTS.md beside what it "+
			"describes. Do not append.", agentContextFile, lines, maxRootLines)
	}
}

// One canonical source: CLAUDE.md points at AGENTS.md instead of drifting from
// it. Two files saying nearly the same thing means an agent reads whichever one
// rotted.
func TestClaudeFileIsOnlyAPointer(t *testing.T) {
	path := filepath.Join(repoRoot, claudePointerFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", claudePointerFile, err)
	}
	if got, want := strings.TrimSpace(string(raw)), "@"+agentContextFile; got != want {
		t.Errorf("%s is %q; it must stay the pointer line %q so there is one "+
			"canonical source.", claudePointerFile, got, want)
	}
}

// frontmatter is the declaration block a SKILL.md opens with. Only name and
// description are part of the contract, and both are single-line scalars — a
// folded or quoted multi-line value is rejected rather than parsed, so the
// files stay readable by anything that reads the first few lines.
func frontmatter(t *testing.T, path string) (name, description string, body []string) {
	t.Helper()
	lines := readLines(t, path)
	if lines[0] != "---" {
		t.Fatalf("%s does not open with a '---' frontmatter block, so nothing "+
			"about the skill is declared and it is never loaded.", path)
	}

	end := -1
	for i, line := range lines[1:] {
		if line == "---" {
			end = i + 1
			break
		}
	}
	if end < 0 {
		t.Fatalf("%s opens a frontmatter block that is never closed.", path)
	}

	for _, line := range lines[1:end] {
		switch key, value, found := strings.Cut(line, ":"); {
		case !found:
			t.Errorf("%s frontmatter line %q is not a single-line 'key: value' pair.", path, line)
		case key == "name":
			name = strings.TrimSpace(value)
		case key == "description":
			description = strings.TrimSpace(value)
		}
	}
	return name, description, lines[end+1:]
}

func TestSkillsDeclareAUsableNameAndDescription(t *testing.T) {
	skills, err := filepath.Glob(filepath.Join(repoRoot, ".claude", "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) == 0 {
		t.Fatal("no skills found under .claude/skills/*/SKILL.md — the layout moved")
	}

	for _, skill := range skills {
		directory := filepath.Base(filepath.Dir(skill))

		t.Run(directory, func(t *testing.T) {
			name, description, body := frontmatter(t, skill)

			if name != directory {
				t.Errorf("%s declares name %q but sits in %q; an agent resolves a "+
					"skill by its directory, so the two must agree.", skill, name, directory)
			}
			if !skillNameShape.MatchString(name) {
				t.Errorf("%s name %q must be lowercase letters, digits and hyphens.", skill, name)
			}
			if len(name) > maxNameChars {
				t.Errorf("%s name is %d characters, over %d.", skill, len(name), maxNameChars)
			}

			switch {
			case description == "":
				t.Errorf("%s has no description — an agent cannot tell when to load it.", skill)
			case len(description) > maxDescriptionSize:
				t.Errorf("%s description is %d characters, over %d.",
					skill, len(description), maxDescriptionSize)
			}

			if len(body) > maxSkillBodyLines {
				t.Errorf("%s body is %d lines, over the %d-line cap. Split the detail "+
					"into reference files the skill reads when it needs them.",
					skill, len(body), maxSkillBodyLines)
			}
		})
	}
}
