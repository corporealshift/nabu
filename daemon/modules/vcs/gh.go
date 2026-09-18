package vcs

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

const ghDescription = "Read GitHub for this repository as structured data. op is one of: " +
	"pr.list, pr.view, issue.list, issue.view, run.list, run.view. " +
	"Use it to see what a pull request says, why a check failed, or what is open. " +
	"It uses whatever gh login is already on this machine; it never handles credentials."

var ghSchema = json.RawMessage(`{"type":"object","required":["op"],"properties":{
	"op":{"type":"string","enum":["pr.list","pr.view","issue.list","issue.view","run.list","run.view"]},
	"number":{"type":"integer","description":"the pull request, issue or run to view"},
	"state":{"type":"string","enum":["open","closed","merged","all"],"description":"list: which to include, default open"},
	"limit":{"type":"integer","description":"list: how many, default 20"}}}`)

type ghArgs struct {
	Op     string `json:"op"`
	Number int    `json:"number"`
	State  string `json:"state"`
	Limit  int    `json:"limit"`
}

// ghFields is what each op asks gh for.
//
// Naming the fields is what makes the reply structured: bare `gh pr view`
// prints a page for a person to read, and `--json` with an explicit field list
// returns the same facts as data. It is also a budget — gh will happily return
// every comment on a long thread, which would cost more context than the
// question is worth.
var ghFields = map[string]string{
	"pr.list":    "number,title,state,isDraft,author,headRefName,baseRefName,createdAt,updatedAt,url",
	"pr.view":    "number,title,state,isDraft,author,headRefName,baseRefName,body,additions,deletions,changedFiles,mergeable,reviewDecision,statusCheckRollup,url",
	"issue.list": "number,title,state,author,labels,createdAt,updatedAt,url",
	"issue.view": "number,title,state,author,labels,body,comments,url",
	"run.list":   "databaseId,name,displayTitle,status,conclusion,headBranch,event,createdAt,url",
	"run.view":   "databaseId,name,displayTitle,status,conclusion,headBranch,event,jobs,url",
}

func (m *Module) runGH(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
	var a ghArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", badArgs("invalid arguments: %s", err)
		}
	}
	if a.Op == "" {
		return "", badArgs("op is required")
	}
	fields, ok := ghFields[a.Op]
	if !ok {
		return "", badArgs("unknown op %q", a.Op)
	}

	args, err := ghArgv(a, fields)
	if err != nil {
		return "", err
	}
	out, err := m.exec(ctx, s, m.ghPath, args...)
	if err != nil {
		return "", err
	}
	// gh already emits JSON, so it is passed through rather than re-encoded:
	// a round trip here would only be a chance to lose a field.
	return m.truncateJSON(out), nil
}

// ghArgv builds the gh command line for one op.
func ghArgv(a ghArgs, fields string) ([]string, error) {
	noun, verb, ok := strings.Cut(a.Op, ".")
	if !ok {
		return nil, badArgs("unknown op %q", a.Op)
	}

	if verb == "view" {
		if a.Number <= 0 {
			return nil, badArgs("%s needs a number", a.Op)
		}
		return []string{noun, "view", strconv.Itoa(a.Number), "--json", fields}, nil
	}

	limit := a.Limit
	if limit <= 0 {
		limit = defaultLogLimit
	}
	if limit > maxLogLimit {
		return nil, badArgs("limit %d is above the maximum of %d", limit, maxLogLimit)
	}
	args := []string{noun, "list", "--limit", strconv.Itoa(limit), "--json", fields}
	// `gh run list` has no --state; its states are status and conclusion, which
	// the caller reads off the rows instead.
	if a.State != "" {
		if noun == "run" {
			return nil, badArgs("run.list has no state; read status and conclusion from the rows")
		}
		args = append(args, "--state", a.State)
	}
	return args, nil
}

// truncateJSON bounds gh's reply without pretending the remainder is still
// JSON. A caller that cannot decode it can see why in the note.
func (m *Module) truncateJSON(out string) string {
	if len(out) <= m.maxOutput {
		return out
	}
	return out[:m.maxOutput] +
		"\n[truncated at " + strconv.Itoa(m.maxOutput) + " bytes; ask for fewer, or view one]"
}
