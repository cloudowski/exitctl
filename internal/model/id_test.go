package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalID(t *testing.T) {
	cases := []struct{ repo, dir, addr, want string }{
		{"github.com/acme/shop", "infra", "aws_dynamodb_table.orders", "github.com/acme/shop//infra#aws_dynamodb_table.orders"},
		{"shop", "", "aws_sqs_queue.q", "shop//#aws_sqs_queue.q"},
		{"shop", ".", "a", "shop//#a"},
		{"shop", "./infra/./prod/", "a", "shop//infra/prod#a"},
		{"shop", "/abs/infra", "a", "shop//abs/infra#a"},
		{"shop", `infra\prod`, "a", "shop//infra/prod#a"},
		{"shop", "../x", "a", "shop//x#a"},
	}
	for _, c := range cases {
		if got := CanonicalID(c.repo, c.dir, c.addr); got != c.want {
			t.Errorf("CanonicalID(%q,%q,%q) = %q, want %q", c.repo, c.dir, c.addr, got, c.want)
		}
	}
}

func TestImpliedServiceID(t *testing.T) {
	if got := ImpliedServiceID("aws.sqs"); got != "implied//aws.sqs" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeRemoteURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/shop.git":                "github.com/acme/shop",
		"https://user:secret@github.com/acme/shop.git":    "github.com/acme/shop",
		"git@github.com:acme/shop.git":                    "github.com/acme/shop",
		"ssh://git@gitlab.example.com:2222/acme/shop.git": "gitlab.example.com/acme/shop",
		"https://GitHub.com/acme/shop/":                   "github.com/acme/shop",
		"git://example.org/acme/shop":                     "example.org/acme/shop",
	}
	for in, want := range cases {
		if got := NormalizeRemoteURL(in); got != want {
			t.Errorf("NormalizeRemoteURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectRepoName(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "myrepo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectRepoName(root, ""); got != "myrepo" {
		t.Errorf("fallback: got %q", got)
	}
	cfg := `[core]
	bare = false
[remote "upstream"]
	url = https://github.com/other/thing.git
[remote "origin"]
	url = git@github.com:acme/shop.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectRepoName(root, ""); got != "github.com/acme/shop" {
		t.Errorf("origin: got %q", got)
	}
	if got := DetectRepoName(root, "custom/name"); got != "custom/name" {
		t.Errorf("override: got %q", got)
	}

	// .git as a gitdir file (worktree / submodule).
	wt := filepath.Join(dir, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ../myrepo/.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectRepoName(wt, ""); got != "github.com/acme/shop" {
		t.Errorf("gitdir file: got %q", got)
	}
}
