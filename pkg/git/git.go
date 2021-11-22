// Copyright (C) 2021 Toitware ApS.
//
// This library is free software; you can redistribute it and/or
// modify it under the terms of the GNU Lesser General Public
// License as published by the Free Software Foundation; version
// 2.1 only.
//
// This library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
// Lesser General Public License for more details.
//
// The license can be found in the file `LICENSE` in the top level
// directory of this repository.

package git

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

type CloneOptions struct {
	URL string
	// Order of preference: hash > branch > tag.
	Hash         string
	Branch       string
	Tag          string
	SingleBranch bool
	Depth        int
	SSHPath      string
}

func convertURLToSSH(str string) (string, error) {
	u, err := url.Parse(str)
	if err != nil {
		return "", err
	}
	return "ssh://git@" + u.Host + ":" + u.Path + ".git", nil
}

// Clone clones the repository with the given [options] into [dir].
// Returns the checked out hash.
func Clone(ctx context.Context, dir string, options CloneOptions) (string, error) {
	url := options.URL
	if !filepath.IsAbs(url) {
		url = "https://" + url
	}
	gogitOptions := &gogit.CloneOptions{
		URL:          url,
		SingleBranch: options.SingleBranch,
		Depth:        options.Depth,
	}

	// It's not easy to clone a specific hash directly.
	// Recent git versions support it (although awkwardly), but go-git doesn't seem to have
	// implemented it yet:
	// ```
	// git init
	// git remote add origin <url>
	// git fetch --depth 1 origin <sha1>
	// git checkout FETCH_HEAD
	// ```
	// If branch or tag is given, we try to checkout that version first. It's likely we
	// will get the right version anyway.
	if options.Branch != "" {
		gogitOptions.ReferenceName = plumbing.NewBranchReferenceName(options.Branch)
	} else if options.Tag != "" {
		gogitOptions.ReferenceName = plumbing.NewTagReferenceName(options.Tag)
	}

	if options.SSHPath != "" {
		// Only try to check out the url with SSH.
		sshURL, err := convertURLToSSH(url)
		if err != nil {
			return "", fmt.Errorf("invalid URL '%s': %v", url, err)
		}
		gogitOptions.URL = sshURL

		auth, err := ssh.NewPublicKeysFromFile("git", options.SSHPath, "")
		if err != nil {
			return "", err
		}
		gogitOptions.Auth = auth

		_, err = gogit.PlainCloneContext(ctx, dir, false, gogitOptions)
		return "", err
	}

	repository, err := gogit.PlainCloneContext(ctx, dir, false, gogitOptions)
	if err == transport.ErrAuthenticationRequired {
		// Try to download the repository with ssh, but without authentication.
		sshURL, errURL := convertURLToSSH(url)
		if errURL != nil {
			gogitOptions.URL = sshURL
			repository, err = gogit.PlainCloneContext(ctx, dir, false, gogitOptions)
		}
	}
	if err != nil && (gogit.NoMatchingRefSpecError{}).Is(err) && options.Hash != "" {
		// The branch/tag doesn't exist, but we have a hash we can try to find directly.
		gogitOptions.Depth = 1
		gogitOptions.ReferenceName = ""
		gogitOptions.NoCheckout = true
		gogitOptions.SingleBranch = false
		_, err = gogit.PlainCloneContext(ctx, dir, false, gogitOptions)
	}
	if err != nil {
		return "", err
	}

	head, err := repository.Head()
	if err != nil {
		return "", err
	}
	downloadedHash := head.Hash().String()
	if options.Hash != "" && downloadedHash != options.Hash {
		w, err := repository.Worktree()
		if err != nil {
			return "", err
		}
		err = w.Checkout(&gogit.CheckoutOptions{
			Hash: plumbing.NewHash(options.Hash),
		})
		if err != nil {
			return "", err
		}
	}
	return head.Hash().String(), nil
}

type PullOptions struct {
	SSHPath string
}

func Pull(path string, options PullOptions) error {
	repository, err := gogit.PlainOpen(path)
	if err != nil {
		return err
	}
	wt, err := repository.Worktree()
	if err != nil {
		return err
	}

	pullOptions := &gogit.PullOptions{
		Force: true,
	}

	if options.SSHPath != "" {
		auth, err := ssh.NewPublicKeysFromFile("git", options.SSHPath, "")
		if err != nil {
			return err
		}
		pullOptions.Auth = auth
	}

	err = wt.Pull(pullOptions)
	if err != nil && err != gogit.NoErrAlreadyUpToDate {
		return err
	}
	return nil
}
