package feature

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/seanturner026/f/internal/model"
)

// ErrBadComment means a comment is missing its file, lines, or note.
var ErrBadComment = errors.New("comment needs file, line1, and note")

// CommentsPath is the feature's pending review comments.
func CommentsPath(l Layout, name string) string {
	return filepath.Join(Dir(l, name), ContextDir, "comments.json")
}

// LoadComments reads pending comments; a missing file is none.
func LoadComments(l Layout, name string) ([]model.Comment, error) {
	raw, err := os.ReadFile(CommentsPath(l, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load comments: %w", err)
	}
	var cs []model.Comment
	if err := json.Unmarshal(raw, &cs); err != nil {
		return nil, fmt.Errorf("load comments: %w", err)
	}
	return cs, nil
}

func saveComments(l Layout, name string, cs []model.Comment) error {
	if cs == nil {
		cs = []model.Comment{}
	}
	if err := writeJSON(CommentsPath(l, name), cs); err != nil {
		return fmt.Errorf("save comments: %w", err)
	}
	return nil
}

// AddComment appends a comment, assigning the next ID.
func AddComment(l Layout, name string, c model.Comment) (model.Comment, error) {
	if c.File == "" || c.Line1 < 1 || c.Note == "" {
		return model.Comment{}, ErrBadComment
	}
	if c.Line2 < c.Line1 {
		c.Line2 = c.Line1
	}
	cs, err := LoadComments(l, name)
	if err != nil {
		return model.Comment{}, err
	}
	for _, x := range cs {
		c.ID = max(c.ID, x.ID)
	}
	c.ID++
	return c, saveComments(l, name, append(cs, c))
}

// ClearComments removes all pending comments.
func ClearComments(l Layout, name string) error { return saveComments(l, name, nil) }
