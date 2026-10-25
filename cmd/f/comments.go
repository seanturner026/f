package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/herdr"
	"github.com/seanturner026/f/internal/model"
)

// commentAdd reads a comment as JSON on stdin (as nvim/f.lua sends it) and saves it.
func commentAdd(d deps, explicit string, in io.Reader) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	var c model.Comment
	if err := json.NewDecoder(in).Decode(&c); err != nil {
		return fmt.Errorf("comment add: read JSON from stdin: %w", err)
	}
	saved, err := feature.AddComment(d.layout, name, c)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(saved)
}

// commentList prints pending comments (JSON for nvim, or readable).
func commentList(d deps, explicit string, asJSON bool) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	cs, err := feature.LoadComments(d.layout, name)
	if err != nil {
		return err
	}
	if asJSON {
		if cs == nil {
			cs = []model.Comment{}
		}
		return json.NewEncoder(os.Stdout).Encode(cs)
	}
	if len(cs) == 0 {
		info("no pending comments")
		return nil
	}
	fmt.Print(model.CommentsMessage(cs))
	return nil
}

func commentClear(d deps, explicit string) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	return feature.ClearComments(d.layout, name)
}

// commentSend sends pending comments to the feature's pi as one prompt and
// clears them. If that fails, they're kept and the message is copied.
func commentSend(ctx context.Context, d deps, explicit string) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	cs, err := feature.LoadComments(d.layout, name)
	if err != nil {
		return err
	}
	if len(cs) == 0 {
		info("no pending comments")
		return nil
	}
	msg := model.CommentsMessage(cs)
	if err := promptPiOrCopy(ctx, d, name, msg); err != nil {
		return fmt.Errorf("%w (comments kept; message copied to clipboard)", err)
	}
	if err := feature.ClearComments(d.layout, name); err != nil {
		return err
	}
	ok("sent %d comment(s) to the agent", len(cs))
	return nil
}

// promptPiOrCopy sends msg to the feature's pi (via Herdr); if that fails,
// msg goes on the clipboard so it isn't lost.
func promptPiOrCopy(ctx context.Context, d deps, name, msg string) error {
	err := herdr.PromptPi(ctx, d.run, feature.Dir(d.layout, name), msg)
	if err != nil {
		_ = copyToClipboard(msg)
	}
	return err
}
