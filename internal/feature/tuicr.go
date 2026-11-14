package feature

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/seanturner026/f/internal/model"
	"github.com/seanturner026/f/internal/tuicr"
)

// ReviewSession is a tuicr session and the comments read from it, so exactly
// those can be deleted once sent (ones added meanwhile stay).
type ReviewSession struct {
	Dir, Slug string
	IDs       []string
}

// TuicrComments gathers the draft comments of every repo's tuicr reviews as
// f comments (file relative to the feature root, code read from the worktree).
// Without tuicr installed there are none.
func TuicrComments(ctx context.Context, r tuicr.Runner, l Layout, name string) ([]model.Comment, []ReviewSession, error) {
	if _, err := exec.LookPath("tuicr"); err != nil {
		return nil, nil, nil
	}
	repos, err := Repos(l, name)
	if err != nil {
		return nil, nil, err
	}
	var out []model.Comment
	var sessions []ReviewSession
	for _, repo := range repos {
		dir := RepoDir(l, name, repo)
		ss, err := tuicr.Sessions(ctx, r, dir)
		if err != nil {
			return nil, nil, err
		}
		for _, s := range ss {
			if s.Comments == 0 {
				continue
			}
			cs, err := tuicr.Comments(ctx, r, dir, s.Slug)
			if err != nil {
				return nil, nil, err
			}
			session := ReviewSession{Dir: dir, Slug: s.Slug}
			for _, c := range cs {
				out = append(out, fromTuicr(dir, repo, c))
				session.IDs = append(session.IDs, c.ID)
			}
			sessions = append(sessions, session)
		}
	}
	return out, sessions, nil
}

// DeleteTuicrComments deletes the comments TuicrComments read.
func DeleteTuicrComments(ctx context.Context, r tuicr.Runner, sessions []ReviewSession) error {
	for _, s := range sessions {
		for _, id := range s.IDs {
			if err := tuicr.DeleteComment(ctx, r, s.Dir, s.Slug, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func fromTuicr(dir, repo string, c tuicr.Comment) model.Comment {
	note := c.Content
	if c.Type != "" && c.Type != "none" {
		note = "[" + c.Type + "] " + note
	}
	m := model.Comment{File: repo, Note: note}
	if c.Path == "" {
		return m // about the whole review
	}
	m.File = repo + "/" + c.Path
	m.Lang = strings.TrimPrefix(filepath.Ext(c.Path), ".")
	if c.StartLine == 0 {
		return m // about the whole file
	}
	m.Line1, m.Line2 = c.StartLine, max(c.EndLine, c.StartLine)
	if c.Side == "old" {
		m.Note = "(on the removed lines) " + m.Note
		return m
	}
	if data, err := os.ReadFile(filepath.Join(dir, c.Path)); err == nil {
		lines := strings.Split(string(data), "\n")
		if m.Line1 <= len(lines) {
			m.Code = strings.Join(lines[m.Line1-1:min(m.Line2, len(lines))], "\n")
		}
	}
	return m
}
