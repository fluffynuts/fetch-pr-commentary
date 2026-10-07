// fetch-pr-commentary dumps all commentary on a GitHub pull request as
// chronological Markdown on stdout, suitable for feeding to an agent.
//
// Interactive prompts render on stderr, so `fetch-pr-commentary > pr.md`
// works while still letting you pick an org / repo / PR.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/fluffynuts/fetch-pr-commentary/internal/appcli"
)

const usageText = `Usage: fetch-pr-commentary [--org ORG] [--repo REPO | --repo ORG/REPO] [--unresolved-only] [--refresh] [PR-NUMBER]

Writes the description, conversation comments, review summaries and inline
review comments of a GitHub pull request to stdout as Markdown, oldest first.
Anything not given on the command line is asked for interactively. Inside a
git repository with a GitHub remote, a bare PR number is enough.

Flags:
`

// Diff hunks on comments deep inside large new files can run to hundreds of
// lines; keep the hunk header plus the lines leading up to the comment.
const maxHunkLines = 20

type options struct {
	org            string
	repo           string
	pr             int
	unresolvedOnly bool
	refresh        bool
}

func init() {
	appcli.Usage = "fetch-pr-commentary [options] [PR-NUMBER]"
	appcli.ExtraHelp = `  --org ORG        organisation or user that owns the repository
  --repo REPO      repository name, or ORG/REPO
  --unresolved-only
                   omit inline comments from resolved review threads
  --refresh        ignore the cached org and repository lists (cached for 1 day)`
	appcli.Examples = []string{
		"fetch-pr-commentary 123 > pr.md",
		"fetch-pr-commentary --repo org/repo --unresolved-only 123",
		"fetch-pr-commentary --version",
		"fetch-pr-commentary --upgrade",
	}
}

func main() {
	// --help, --version, --install and --upgrade live in internal/appcli.
	if handled, exitCode := appcli.Handle(os.Args[1:]); handled {
		os.Exit(exitCode)
	}
	opts := parseArgs(os.Args[1:])
	if err := run(opts, os.Stdout); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "fetch-pr-commentary:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------- arguments

func parseArgs(args []string) options {
	var o options
	fs := flag.NewFlagSet("fetch-pr-commentary", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&o.org, "org", "", "organisation or user that owns the repository")
	fs.StringVar(&o.repo, "repo", "", "repository name, or ORG/REPO")
	fs.BoolVar(&o.unresolvedOnly, "unresolved-only", false, "omit inline comments from resolved review threads")
	fs.BoolVar(&o.refresh, "refresh", false, "ignore cached organisation and repository lists (cached for 1 day)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usageText)
		fs.PrintDefaults()
	}

	// The flag package stops at the first positional argument; keep going so
	// flags may appear before or after the PR number.
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				os.Exit(0)
			}
			os.Exit(2) // flag has already printed the error and usage
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}

	switch len(positional) {
	case 0:
	case 1:
		n, err := strconv.Atoi(strings.TrimPrefix(positional[0], "#"))
		if err != nil || n <= 0 {
			usageError("PR number must be a positive integer, got %q", positional[0])
		}
		o.pr = n
	default:
		usageError("expected at most one PR number, got %q", positional)
	}

	if owner, name, ok := strings.Cut(o.repo, "/"); ok {
		if owner == "" || name == "" || strings.Contains(name, "/") {
			usageError("--repo must be REPO or ORG/REPO, got %q", o.repo)
		}
		if o.org != "" && !strings.EqualFold(o.org, owner) {
			usageError("--org %q conflicts with --repo %q", o.org, o.repo)
		}
		o.org, o.repo = owner, name
	}
	return o
}

func usageError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fetch-pr-commentary: "+format+"\n\n", args...)
	fmt.Fprint(os.Stderr, usageText)
	os.Exit(2)
}

// ---------------------------------------------------------------- flow

func run(o options, out io.Writer) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("the gh CLI is required but was not found on PATH")
	}

	refreshCache = o.refresh
	remoteOrg, remoteRepo := repoFromGitRemote()

	// Inside a GitHub checkout, a bare PR number needs no further input.
	if o.org == "" && o.repo == "" && o.pr != 0 && remoteOrg != "" {
		o.org, o.repo = remoteOrg, remoteRepo
	}

	if (o.org == "" || o.repo == "" || o.pr == 0) && !stdinIsTerminal() {
		return errors.New("not running interactively: pass --org, --repo and a PR number")
	}

	var err error
	if o.org == "" {
		if o.org, err = pickOrg(remoteOrg); err != nil {
			return err
		}
	}
	if o.repo == "" {
		defaultRepo := ""
		if strings.EqualFold(o.org, remoteOrg) {
			defaultRepo = remoteRepo
		}
		if o.repo, err = pickRepo(o.org, defaultRepo); err != nil {
			return err
		}
	}
	if o.pr == 0 {
		if o.pr, err = pickPR(o.org, o.repo); err != nil {
			return err
		}
	}

	status("Fetching commentary for %s/%s#%d…", o.org, o.repo, o.pr)
	c, err := fetchCommentary(o.org, o.repo, o.pr)
	if err != nil {
		return err
	}
	return writeMarkdown(out, c, o.unresolvedOnly)
}

// ---------------------------------------------------------------- git remote

var githubRemote = regexp.MustCompile(`github\.com[:/]+([^/]+)/([^/]+?)(?:\.git)?/?$`)

// repoFromGitRemote returns the GitHub owner/name for the current checkout,
// or empty strings when not in a git repo with a github.com remote.
// Preference: the remote chosen via `gh repo set-default`, then upstream
// (where PRs from a fork usually live), then origin.
func repoFromGitRemote() (string, string) {
	var remotes []string
	if out, err := exec.Command("git", "config", "--get-regexp", `^remote\..*\.gh-resolved$`).Output(); err == nil {
		for _, l := range lines(out) {
			key, val, _ := strings.Cut(l, " ")
			if owner, name, ok := strings.Cut(val, "/"); ok {
				return owner, name // newer gh stores OWNER/REPO directly
			}
			if val == "base" {
				remotes = append(remotes, strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".gh-resolved"))
			}
		}
	}
	remotes = append(remotes, "upstream", "origin")

	for _, remote := range remotes {
		url, err := exec.Command("git", "remote", "get-url", remote).Output()
		if err != nil {
			continue
		}
		if m := githubRemote.FindStringSubmatch(strings.TrimSpace(string(url))); m != nil {
			return m[1], m[2]
		}
	}
	return "", ""
}

// ---------------------------------------------------------------- pickers

func pickOrg(defaultOrg string) (string, error) {
	choices, err := cached("orgs", func() ([]string, error) {
		status("Loading organisations…")
		login, err := gh("api", "user", "--jq", ".login")
		if err != nil {
			return nil, err
		}
		orgsRaw, err := gh("api", "user/orgs", "--paginate", "--jq", ".[].login")
		if err != nil {
			return nil, err
		}
		orgs := lines(orgsRaw)
		sortFold(orgs)
		return append([]string{strings.TrimSpace(string(login))}, orgs...), nil
	})
	if err != nil {
		return "", err
	}
	if defaultOrg != "" && !containsFold(choices, defaultOrg) {
		choices = append(choices, defaultOrg) // e.g. a public repo you're not a member of
	}
	return pick("Organisation", stringOptions(choices, defaultOrg), defaultOrg)
}

func pickRepo(org string, defaultRepo string) (string, error) {
	repos, err := cached("repos-"+org, func() ([]string, error) {
		status("Loading repositories for %s…", org)
		raw, err := gh("repo", "list", org, "--limit", "5000", "--json", "name", "--jq", ".[].name")
		if err != nil {
			return nil, err
		}
		repos := lines(raw)
		sortFold(repos)
		return repos, nil
	})
	if err != nil {
		return "", err
	}
	if defaultRepo != "" && !containsFold(repos, defaultRepo) {
		repos = append([]string{defaultRepo}, repos...)
	}
	if len(repos) == 0 {
		return "", fmt.Errorf("no repositories visible in %s", org)
	}
	return pick("Repository in "+org, stringOptions(repos, defaultRepo), defaultRepo)
}

func pickPR(org string, repo string) (int, error) {
	status("Loading open pull requests for %s/%s…", org, repo)
	raw, err := gh("pr", "list", "-R", org+"/"+repo, "--state", "open", "--limit", "1000", "--json", "number,title")
	if err != nil {
		return 0, err
	}
	var prs []struct {
		Number int
		Title  string
	}
	if err := json.Unmarshal(raw, &prs); err != nil {
		return 0, fmt.Errorf("decoding PR list: %w", err)
	}
	if len(prs) == 0 {
		return 0, fmt.Errorf("%s/%s has no open pull requests", org, repo)
	}
	options := make([]huh.Option[int], 0, len(prs))
	for _, p := range prs {
		options = append(options, huh.NewOption(fmt.Sprintf("#%d %s", p.Number, p.Title), p.Number))
	}
	return pick("Pull request", options, 0)
}

func stringOptions(choices []string, defaultChoice string) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(choices))
	for _, c := range choices {
		opt := huh.NewOption(c, c)
		if defaultChoice != "" && strings.EqualFold(c, defaultChoice) {
			opt = opt.Selected(true)
		}
		options = append(options, opt)
	}
	return options
}

func pick[T comparable](title string, options []huh.Option[T], defaultValue T) (T, error) {
	value := defaultValue
	sel := huh.NewSelect[T]().
		Title(title).
		Description("type to filter · ↑/↓ move · enter choose").
		Filtering(true).
		Value(&value).
		Options(options...)
	if len(options) > 12 {
		sel = sel.Height(14)
	}
	err := huh.NewForm(huh.NewGroup(sel)).WithOutput(os.Stderr).Run()
	return value, err
}

// ---------------------------------------------------------------- GitHub data

// Field names rely on encoding/json's case-insensitive matching
// (URL <- "url", CreatedAt <- "createdAt").

type author struct{ Login string }

type pullRequest struct {
	Number      int
	Title       string
	URL         string
	Body        string
	State       string
	IsDraft     bool
	CreatedAt   time.Time
	BaseRefName string
	HeadRefName string
	Author      *author
}

type issueComment struct {
	Author    *author
	Body      string
	CreatedAt time.Time
	URL       string
}

type review struct {
	Author      *author
	Body        string
	State       string
	CreatedAt   time.Time
	SubmittedAt *time.Time
	URL         string
}

type threadComment struct {
	Author    *author
	Body      string
	DiffHunk  string
	CreatedAt time.Time
	URL       string
}

type reviewThread struct {
	ID                string
	IsResolved        bool
	IsOutdated        bool
	Path              string
	Line              *int
	OriginalLine      *int
	StartLine         *int
	OriginalStartLine *int
	ResolvedBy        *author
	Comments          struct {
		TotalCount int
		Nodes      []threadComment
	}
}

type commentary struct {
	PR       pullRequest
	Comments []issueComment
	Reviews  []review
	Threads  []reviewThread
}

const prHeaderQuery = `query($owner: String!, $repo: String!, $pr: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $pr) {
      number title url body state isDraft createdAt baseRefName headRefName author { login }
    }
  }
}`

const connectionQuery = `query($owner: String!, $repo: String!, $pr: Int!, $endCursor: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $pr) {
      %s(first: 100, after: $endCursor) {
        pageInfo { hasNextPage endCursor }
        nodes { %s }
      }
    }
  }
}`

const (
	issueCommentFields = `author { login } body createdAt url`
	reviewFields       = `author { login } body state createdAt submittedAt url`
	threadFields       = `id isResolved isOutdated path line originalLine startLine originalStartLine
        resolvedBy { login }
        comments(first: 100) { totalCount nodes { author { login } body diffHunk createdAt url } }`
)

func fetchCommentary(owner string, repo string, pr int) (*commentary, error) {
	var header struct {
		Repository struct{ PullRequest *pullRequest }
	}
	if err := graphql(prHeaderQuery, owner, repo, pr, "", &header); err != nil {
		return nil, err
	}
	if header.Repository.PullRequest == nil {
		return nil, fmt.Errorf("%s/%s#%d not found", owner, repo, pr)
	}

	c := &commentary{PR: *header.Repository.PullRequest}
	var err error
	if c.Comments, err = fetchAll[issueComment](owner, repo, pr, "comments", issueCommentFields); err != nil {
		return nil, err
	}
	if c.Reviews, err = fetchAll[review](owner, repo, pr, "reviews", reviewFields); err != nil {
		return nil, err
	}
	if c.Threads, err = fetchAll[reviewThread](owner, repo, pr, "reviewThreads", threadFields); err != nil {
		return nil, err
	}
	for _, t := range c.Threads {
		if t.Comments.TotalCount > len(t.Comments.Nodes) {
			status("warning: thread %s on %s has %d comments; only the first %d are included",
				t.ID, t.Path, t.Comments.TotalCount, len(t.Comments.Nodes))
		}
	}
	return c, nil
}

// fetchAll walks one paginated connection on the pull request.
func fetchAll[T any](owner string, repo string, pr int, connection string, fields string) ([]T, error) {
	query := fmt.Sprintf(connectionQuery, connection, fields)
	var all []T
	cursor := ""
	for {
		var data struct {
			Repository struct {
				PullRequest map[string]struct {
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
					Nodes []T
				}
			}
		}
		if err := graphql(query, owner, repo, pr, cursor, &data); err != nil {
			return nil, fmt.Errorf("fetching %s: %w", connection, err)
		}
		page := data.Repository.PullRequest[connection]
		all = append(all, page.Nodes...)
		if !page.PageInfo.HasNextPage {
			return all, nil
		}
		cursor = page.PageInfo.EndCursor
	}
}

func graphql(query string, owner string, repo string, pr int, cursor string, out any) error {
	// -f sends raw strings (so a numeric org name stays a string); -F types pr as Int.
	args := []string{"api", "graphql",
		"-f", "query=" + query,
		"-f", "owner=" + owner,
		"-f", "repo=" + repo,
		"-F", fmt.Sprintf("pr=%d", pr),
	}
	if cursor != "" {
		args = append(args, "-f", "endCursor="+cursor)
	}
	raw, err := gh(args...)
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decoding GraphQL response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			msgs = append(msgs, e.Message)
		}
		return errors.New(strings.Join(msgs, "; "))
	}
	return json.Unmarshal(envelope.Data, out)
}

func gh(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh %s: %s", args[0], msg)
	}
	return out, nil
}

// ---------------------------------------------------------------- rendering

type entry struct {
	at   time.Time
	text string
}

func writeMarkdown(w io.Writer, c *commentary, unresolvedOnly bool) error {
	var b strings.Builder
	pr := c.PR
	state := strings.ToLower(pr.State)
	if pr.IsDraft {
		state += " (draft)"
	}
	fmt.Fprintf(&b, "# PR #%d: %s\n\n", pr.Number, pr.Title)
	fmt.Fprintf(&b, "- URL: %s\n- Author: %s\n- State: %s\n- Branches: %s → %s\n- Opened: %s\n\n",
		pr.URL, loginOf(pr.Author), state, pr.HeadRefName, pr.BaseRefName, stamp(pr.CreatedAt))
	fmt.Fprintf(&b, "## Description\n\n%s\n\n## Commentary (oldest first)\n", orPlaceholder(pr.Body, "_No description provided._"))

	var entries []entry
	for _, ic := range c.Comments {
		entries = append(entries, entry{ic.CreatedAt, fmt.Sprintf("### Comment by %s · %s\n\n%s\n",
			loginOf(ic.Author), stamp(ic.CreatedAt), orPlaceholder(ic.Body, "_(empty)_"))})
	}
	for _, r := range c.Reviews {
		// Pending reviews are unsubmitted drafts; empty COMMENTED reviews are
		// just the envelope for inline comments, which appear on their own.
		if r.State == "PENDING" || (r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "") {
			continue
		}
		at := r.CreatedAt
		if r.SubmittedAt != nil {
			at = *r.SubmittedAt
		}
		entries = append(entries, entry{at, fmt.Sprintf("### Review by %s · %s · %s\n\n%s\n",
			loginOf(r.Author), strings.ToLower(strings.ReplaceAll(r.State, "_", " ")), stamp(at),
			orPlaceholder(r.Body, "_No summary._"))})
	}
	for _, t := range c.Threads {
		if unresolvedOnly && t.IsResolved {
			continue
		}
		for i, tc := range t.Comments.Nodes {
			entries = append(entries, entry{tc.CreatedAt, renderThreadComment(t, tc, i == 0)})
		}
	}

	sort.SliceStable(entries, func(i int, j int) bool { return entries[i].at.Before(entries[j].at) })
	if len(entries) == 0 {
		b.WriteString("\n_No commentary._\n")
	}
	for _, e := range entries {
		b.WriteString("\n---\n\n")
		b.WriteString(e.text)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func renderThreadComment(t reviewThread, c threadComment, first bool) string {
	var b strings.Builder
	kind := "Inline comment"
	if !first {
		kind = "Reply"
	}
	threadState := "unresolved"
	if t.IsResolved {
		threadState = "resolved"
		if t.ResolvedBy != nil {
			threadState += " by " + t.ResolvedBy.Login
		}
	}
	if t.IsOutdated {
		threadState += ", outdated: the code has changed since"
	}
	fmt.Fprintf(&b, "### %s by %s · %s\n\n", kind, loginOf(c.Author), stamp(c.CreatedAt))
	fmt.Fprintf(&b, "- Location: `%s` %s\n- Thread: %s (%s)\n\n", t.Path, lineRef(t), t.ID, threadState)
	if first && c.DiffHunk != "" {
		hunk := trimHunk(c.DiffHunk, maxHunkLines)
		f := fence(hunk)
		fmt.Fprintf(&b, "%sdiff\n%s\n%s\n\n", f, hunk, f)
	}
	b.WriteString(orPlaceholder(c.Body, "_(empty)_"))
	b.WriteString("\n")
	return b.String()
}

func lineRef(t reviewThread) string {
	end, start, suffix := t.Line, t.StartLine, ""
	if end == nil {
		end, start, suffix = t.OriginalLine, t.OriginalStartLine, " (in the original diff)"
	}
	switch {
	case end == nil:
		return "(file-level)"
	case start != nil && *start != *end:
		return fmt.Sprintf("lines %d–%d%s", *start, *end, suffix)
	default:
		return fmt.Sprintf("line %d%s", *end, suffix)
	}
}

func trimHunk(hunk string, keep int) string {
	ls := strings.Split(strings.TrimRight(hunk, "\n"), "\n")
	if len(ls) <= keep+1 {
		return strings.Join(ls, "\n")
	}
	return ls[0] + "\n ...\n" + strings.Join(ls[len(ls)-keep:], "\n")
}

// fence returns a code fence longer than any backtick run in s.
func fence(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// ---------------------------------------------------------------- helpers

func loginOf(a *author) string {
	if a == nil || a.Login == "" {
		return "ghost" // deleted account
	}
	return a.Login
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func orPlaceholder(s string, placeholder string) string {
	if s = strings.TrimSpace(s); s == "" {
		return placeholder
	}
	return s
}

func status(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func lines(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func sortFold(s []string) {
	sort.Slice(s, func(i int, j int) bool { return strings.ToLower(s[i]) < strings.ToLower(s[j]) })
}

func containsFold(s []string, want string) bool {
	for _, v := range s {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
